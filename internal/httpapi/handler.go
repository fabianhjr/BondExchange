package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	bondexchangev1 "github.com/fabianhjr/BondExchange/gen/go/bondexchange/v1"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
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
	)
	if err := bondexchangev1.RegisterBondExchangeServiceHandlerServer(context.Background(), mux, server); err != nil {
		return nil, err
	}
	return requireSingleJSONDocument(mux), nil
}

func requireSingleJSONDocument(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			writeRESTError(response, http.StatusBadRequest, "invalid JSON request")
			return
		}
		request.Body = io.NopCloser(bytes.NewReader(body))
		if len(bytes.TrimSpace(body)) == 0 {
			next.ServeHTTP(response, request)
			return
		}
		decoder := json.NewDecoder(bytes.NewReader(body))
		var document json.RawMessage
		if err := decoder.Decode(&document); err != nil {
			writeRESTError(response, http.StatusBadRequest, "invalid JSON request")
			return
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			writeRESTError(response, http.StatusBadRequest, "request must contain one JSON object")
			return
		}
		next.ServeHTTP(response, request)
	})
}

func setSuccessStatus(_ context.Context, response http.ResponseWriter, message proto.Message) error {
	if _, ok := message.(*bondexchangev1.BuyResponse); ok {
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
	writeRESTError(response, runtime.HTTPStatusFromCode(grpcStatus.Code()), grpcStatus.Message())
}

func writeRESTError(response http.ResponseWriter, httpStatus int, message string) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(httpStatus)
	_ = json.NewEncoder(response).Encode(&bondexchangev1.Error{Error: message})
}
