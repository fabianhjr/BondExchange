package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	bondexchangev1 "github.com/fabianhjr/BondExchange/application/gen/go/bondexchange/v1"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func NewHandler(server bondexchangev1.BondExchangeServiceServer) (http.Handler, error) {
	mux := runtime.NewServeMux(
		runtime.WithMarshalerOption(runtime.MIMEWildcard, &runtime.JSONPb{
			MarshalOptions: protojson.MarshalOptions{
				UseProtoNames:   true,
				EmitUnpopulated: true,
			},
			UnmarshalOptions: protojson.UnmarshalOptions{DiscardUnknown: false},
		}),
		runtime.WithErrorHandler(writeError),
		runtime.WithForwardResponseOption(setSuccessStatus),
		runtime.WithMetadata(forwardAuthenticationMetadata),
	)
	if err := bondexchangev1.RegisterBondExchangeServiceHandlerServer(context.Background(), mux, server); err != nil {
		return nil, err
	}
	handler := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/active-offers" {
			serveActiveOffersStream(response, request, server)
			return
		}
		mux.ServeHTTP(response, request)
	})
	secured := recoverPanics(securityHeaders(requireSingleJSONDocument(handler)))
	return instrumentHTTP(secured), nil
}

type originalRequestKey struct{}

func instrumentHTTP(next http.Handler) http.Handler {
	instrumented := otelhttp.NewHandler(
		http.HandlerFunc(func(response http.ResponseWriter, sanitized *http.Request) {
			trace.SpanFromContext(sanitized.Context()).SetAttributes(attribute.String("http.route", sanitized.URL.Path))
			if labeler, found := otelhttp.LabelerFromContext(sanitized.Context()); found {
				labeler.Add(attribute.String("http.route", sanitized.URL.Path))
			}
			original, ok := sanitized.Context().Value(originalRequestKey{}).(*http.Request)
			if !ok {
				next.ServeHTTP(response, sanitized)
				return
			}
			next.ServeHTTP(response, original.Clone(sanitized.Context()))
		}),
		"bondexchange.http",
		otelhttp.WithSpanNameFormatter(func(_ string, request *http.Request) string {
			return request.Method + " " + request.URL.Path
		}),
	)
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		route := routeTemplate(request.URL.Path)
		sanitized := request.Clone(context.WithValue(request.Context(), originalRequestKey{}, request))
		sanitizedURL := *request.URL
		sanitizedURL.Path = route
		sanitizedURL.RawPath = ""
		sanitizedURL.RawQuery = ""
		sanitizedURL.ForceQuery = false
		sanitizedURL.Fragment = ""
		sanitizedURL.RawFragment = ""
		sanitized.URL = &sanitizedURL
		sanitized.Method = methodTemplate(request.Method)
		sanitized.Host = "bond-exchange"
		sanitized.RemoteAddr = ""
		sanitized.RequestURI = route
		sanitized.ContentLength = 0
		sanitized.Header = make(http.Header)
		for _, name := range []string{"Traceparent", "Tracestate", "Baggage"} {
			if values := request.Header.Values(name); len(values) > 0 {
				sanitized.Header[name] = append([]string(nil), values...)
			}
		}
		instrumented.ServeHTTP(response, sanitized)
	})
}

func methodTemplate(method string) string {
	switch method {
	case http.MethodGet, http.MethodPost:
		return method
	default:
		return "OTHER"
	}
}

func routeTemplate(path string) string {
	switch path {
	case "/buys", "/sale-offer-quotes", "/sale-offers", "/active-offers", "/active-bond-series", "/healthz", "/event-publications:publish-pending":
		return path
	default:
		return "unmatched"
	}
}

func recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		defer func() { //nolint:contextcheck // The recovery closure intentionally uses its enclosing request context.
			if recovered := recover(); recovered != nil {
				slog.ErrorContext(request.Context(), "recovered HTTP panic", "method", request.Method, "path", request.URL.Path)
				writeRESTError(response, http.StatusInternalServerError, "internal server error")
			}
		}()
		next.ServeHTTP(response, request)
	})
}

func forwardAuthenticationMetadata(_ context.Context, request *http.Request) metadata.MD {
	result := metadata.MD{}
	for _, mapping := range []struct {
		header string
		key    string
	}{
		{header: "Idempotency-Key", key: "idempotency-key"},
	} {
		for _, value := range request.Header.Values(mapping.header) {
			result.Append(mapping.key, value)
		}
	}
	return result
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Cache-Control", "no-store")
		response.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(&secureResponseWriter{ResponseWriter: response}, request)
	})
}

type secureResponseWriter struct {
	http.ResponseWriter
}

func (writer *secureResponseWriter) WriteHeader(statusCode int) {
	if writer.Header().Get("Content-Type") == "application/json" {
		writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	}
	writer.ResponseWriter.WriteHeader(statusCode)
}

func (writer *secureResponseWriter) Write(payload []byte) (int, error) {
	if writer.Header().Get("Content-Type") == "application/json" {
		writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	}
	return writer.ResponseWriter.Write(payload)
}

func (writer *secureResponseWriter) Flush() {
	if flusher, ok := writer.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (writer *secureResponseWriter) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}

type activeOffersRESTStream struct {
	//nolint:containedctx // grpc.ServerStream requires the request context to remain available for the stream lifetime.
	context  context.Context
	response http.ResponseWriter
	started  bool
}

func serveActiveOffersStream(
	response http.ResponseWriter,
	request *http.Request,
	server bondexchangev1.BondExchangeServiceServer,
) {
	if request.Method != http.MethodGet {
		logRejectedInput(request, "unsupported_method")
		response.Header().Set("Allow", http.MethodGet)
		writeRESTError(response, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if request.Body != nil && request.Body != http.NoBody && request.ContentLength != 0 {
		logRejectedInput(request, "body_on_get")
		writeRESTError(response, http.StatusBadRequest, "request body is not allowed")
		return
	}
	query := request.URL.Query()
	if len(query) != 1 || len(query["bond"]) != 1 {
		logRejectedInput(request, "query_parameter_pollution")
		writeRESTError(response, http.StatusBadRequest, "exactly one bond query parameter is required")
		return
	}
	incoming := forwardAuthenticationMetadata(request.Context(), request)
	for _, value := range request.Header.Values("Authorization") {
		incoming.Append("authorization", value)
	}
	ctx := metadata.NewIncomingContext(request.Context(), incoming)
	stream := &activeOffersRESTStream{context: ctx, response: response}
	err := server.ListActiveOffers(
		&bondexchangev1.ListActiveOffersRequest{Bond: query.Get("bond")},
		stream,
	)
	if err == nil {
		return
	}
	converted := status.Convert(err)
	if !stream.started {
		writeStatusError(response, converted)
		return
	}
	_ = stream.writeJSONSequence(&bondexchangev1.Error{Error: converted.Message()})
}

func (stream *activeOffersRESTStream) Send(message *bondexchangev1.ListActiveOffersResponse) error {
	return stream.writeJSONSequence(message)
}

func (stream *activeOffersRESTStream) writeJSONSequence(message proto.Message) error {
	if !stream.started {
		stream.response.Header().Set("Content-Type", "application/json-seq; charset=utf-8")
		stream.response.WriteHeader(http.StatusOK)
		stream.started = true
	}
	payload, err := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(message)
	if err != nil {
		return err
	}
	_ = http.NewResponseController(stream.response).SetWriteDeadline(time.Now().Add(30 * time.Second))
	if _, err := stream.response.Write(append([]byte{0x1e}, append(payload, '\n')...)); err != nil {
		return err
	}
	if flusher, ok := stream.response.(http.Flusher); ok {
		flusher.Flush()
	}
	return nil
}

func (stream *activeOffersRESTStream) SetHeader(metadata.MD) error  { return nil }
func (stream *activeOffersRESTStream) SendHeader(metadata.MD) error { return nil }
func (stream *activeOffersRESTStream) SetTrailer(metadata.MD)       {}
func (stream *activeOffersRESTStream) Context() context.Context     { return stream.context }
func (stream *activeOffersRESTStream) SendMsg(message any) error {
	typed, ok := message.(*bondexchangev1.ListActiveOffersResponse)
	if !ok {
		return errors.New("unexpected active-offer stream message")
	}
	return stream.Send(typed)
}
func (stream *activeOffersRESTStream) RecvMsg(any) error { return io.EOF }

func requireSingleJSONDocument(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Body == nil || request.Method == http.MethodGet || request.Method == http.MethodHead {
			next.ServeHTTP(response, request)
			return
		}
		mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
		if err != nil || !strings.EqualFold(mediaType, "application/json") {
			logRejectedInput(request, "unsupported_media_type")
			writeRESTError(response, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
			return
		}
		request.Body = http.MaxBytesReader(response, request.Body, 64*1024)
		body, err := io.ReadAll(request.Body)
		if err != nil {
			logRejectedInput(request, "request_too_large")
			writeRESTError(response, http.StatusRequestEntityTooLarge, "JSON request exceeds 65536 bytes")
			return
		}
		request.Body = io.NopCloser(bytes.NewReader(body))
		if len(bytes.TrimSpace(body)) == 0 {
			next.ServeHTTP(response, request)
			return
		}
		if err := validateSingleJSONObject(body); err != nil {
			logRejectedInput(request, "invalid_json_structure")
			writeRESTError(response, http.StatusBadRequest, "invalid JSON request")
			return
		}
		next.ServeHTTP(response, request)
	})
}

func logRejectedInput(request *http.Request, reason string) {
	slog.WarnContext(request.Context(), "request rejected", "event", "security.input_validation", "method", request.Method, "path", request.URL.Path, "reason", reason)
}

func validateSingleJSONObject(body []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	first, err := decoder.Token()
	if err != nil || first != json.Delim('{') {
		return errors.New("top-level JSON value must be an object")
	}
	if err := validateJSONObject(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("request must contain one JSON object")
	}
	return nil
}

func validateJSONObject(decoder *json.Decoder) error {
	keys := make(map[string]struct{})
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return err
		}
		name, ok := key.(string)
		if !ok {
			return errors.New("JSON object key must be a string")
		}
		if _, duplicate := keys[name]; duplicate {
			return errors.New("duplicate JSON object key")
		}
		keys[name] = struct{}{}
		value, err := decoder.Token()
		if err != nil {
			return err
		}
		if err := validateJSONValue(decoder, value); err != nil {
			return err
		}
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim('}') {
		return errors.New("invalid JSON object")
	}
	return nil
}

func validateJSONValue(decoder *json.Decoder, value json.Token) error {
	delimiter, ok := value.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		return validateJSONObject(decoder)
	case '[':
		for decoder.More() {
			item, err := decoder.Token()
			if err != nil {
				return err
			}
			if err := validateJSONValue(decoder, item); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return errors.New("invalid JSON array")
		}
		return nil
	default:
		return errors.New("unexpected JSON delimiter")
	}
}

func setSuccessStatus(_ context.Context, response http.ResponseWriter, message proto.Message) error {
	switch message.(type) {
	case *bondexchangev1.BuyResponse,
		*bondexchangev1.QuoteSaleOfferResponse,
		*bondexchangev1.CreateSaleOfferResponse:
		response.WriteHeader(http.StatusCreated)
	}
	return nil
}

func writeError(
	_ context.Context,
	_ *runtime.ServeMux,
	_ runtime.Marshaler,
	response http.ResponseWriter,
	_ *http.Request,
	err error,
) {
	grpcStatus := status.Convert(err)
	writeStatusError(response, grpcStatus)
}

func writeStatusError(response http.ResponseWriter, grpcStatus *status.Status) {
	for _, detail := range grpcStatus.Details() {
		retry, ok := detail.(*errdetails.RetryInfo)
		if !ok || retry.GetRetryDelay() == nil {
			continue
		}
		delay := retry.GetRetryDelay().AsDuration()
		seconds := int64(delay / time.Second)
		if delay%time.Second != 0 {
			seconds++
		}
		if seconds > 0 {
			response.Header().Set("Retry-After", strconv.FormatInt(seconds, 10))
		}
		break
	}
	writeRESTError(response, runtime.HTTPStatusFromCode(grpcStatus.Code()), grpcStatus.Message())
}

func writeRESTError(response http.ResponseWriter, httpStatus int, message string) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(httpStatus)
	if err := json.NewEncoder(response).Encode(&bondexchangev1.Error{Error: message}); err != nil {
		slog.Warn("failed to write REST error response", "error", err)
	}
}
