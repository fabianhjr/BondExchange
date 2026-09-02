package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/fabianhjr/BondExchange/internal/exchange"
)

type HealthChecker interface {
	Ping(ctx context.Context) error
}

type Application interface {
	Buy(ctx context.Context, buyer string, offer string) (exchange.Purchase, error)
	ActiveOffers(ctx context.Context, bond string, after string, limit int) ([]exchange.SaleOffer, error)
}

type Handler struct {
	application Application
	health      HealthChecker
}

func NewHandler(application Application, health HealthChecker) http.Handler {
	handler := &Handler{application: application, health: health}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /buys", handler.buy)
	mux.HandleFunc("GET /active-offers", handler.activeOffers)
	mux.HandleFunc("GET /healthz", handler.healthz)
	return mux
}

type buyRequest struct {
	BuyerID     string `json:"buyer_id"`
	SaleOfferID string `json:"sale_offer_id"`
}

func (handler *Handler) buy(response http.ResponseWriter, request *http.Request) {
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var input buyRequest
	if err := decoder.Decode(&input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid JSON request")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(response, http.StatusBadRequest, "request must contain one JSON object")
		return
	}

	purchase, err := handler.application.Buy(
		request.Context(),
		input.BuyerID,
		input.SaleOfferID,
	)
	if err != nil {
		switch {
		case errors.Is(err, exchange.ErrInvalidUserID),
			errors.Is(err, exchange.ErrInvalidOfferID):
			writeError(response, http.StatusBadRequest, err.Error())
		case errors.Is(err, exchange.ErrBuyerNotFound),
			errors.Is(err, exchange.ErrOfferUnavailable):
			writeError(response, http.StatusNotFound, err.Error())
		default:
			writeError(response, http.StatusInternalServerError, "internal server error")
		}
		return
	}
	writeJSON(response, http.StatusCreated, purchase)
}

func (handler *Handler) activeOffers(response http.ResponseWriter, request *http.Request) {
	limit := 0
	if rawLimit := request.URL.Query().Get("limit"); rawLimit != "" {
		parsedLimit, err := strconv.Atoi(rawLimit)
		if err != nil {
			writeError(response, http.StatusBadRequest, "limit must be an integer")
			return
		}
		limit = parsedLimit
	}
	offers, err := handler.application.ActiveOffers(
		request.Context(),
		request.URL.Query().Get("bond"),
		request.URL.Query().Get("after"),
		limit,
	)
	if err != nil {
		switch {
		case errors.Is(err, exchange.ErrInvalidBondSeries),
			errors.Is(err, exchange.ErrInvalidOfferID),
			errors.Is(err, exchange.ErrInvalidActiveOfferLimit):
			writeError(response, http.StatusBadRequest, err.Error())
		default:
			writeError(response, http.StatusInternalServerError, "internal server error")
		}
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"offers": offers})
}

func (handler *Handler) healthz(response http.ResponseWriter, request *http.Request) {
	if err := handler.health.Ping(request.Context()); err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(response, http.StatusOK, map[string]string{"status": "ok"})
}

func writeError(response http.ResponseWriter, status int, message string) {
	writeJSON(response, status, map[string]string{"error": message})
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}
