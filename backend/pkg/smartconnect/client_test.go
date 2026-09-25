package smartconnect

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func testClient(rootURL string, timeout time.Duration) *SmartConnect {
	return &SmartConnect{
		rootURL:    rootURL,
		accept:     "application/json",
		httpClient: &http.Client{Timeout: timeout},
	}
}

func TestPlaceOrder_TimeoutIsOutcomeUnknown(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer srv.Close()
	defer close(release)

	sc := testClient(srv.URL, 50*time.Millisecond)
	_, err := sc.PlaceOrder(map[string]any{"tradingsymbol": "X"})
	if !errors.Is(err, ErrOutcomeUnknown) {
		t.Fatalf("timeout must be ErrOutcomeUnknown, got %v", err)
	}
}

func TestPlaceOrder_GatewayErrorIsOutcomeUnknown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("<html>502 Bad Gateway</html>"))
	}))
	defer srv.Close()

	_, err := testClient(srv.URL, time.Second).PlaceOrder(map[string]any{})
	if !errors.Is(err, ErrOutcomeUnknown) {
		t.Fatalf("5xx non-JSON must be ErrOutcomeUnknown, got %v", err)
	}
}

func TestPlaceOrder_BrokerRejectionIsDefinitive(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":false,"message":"Invalid Token","errorcode":"AG8001","data":null}`))
	}))
	defer srv.Close()

	_, err := testClient(srv.URL, time.Second).PlaceOrder(map[string]any{})
	if err == nil {
		t.Fatal("expected rejection error")
	}
	if errors.Is(err, ErrOutcomeUnknown) {
		t.Fatalf("broker JSON rejection must not be ErrOutcomeUnknown: %v", err)
	}
}

func TestPlaceOrder_StatusTrueWithoutOrderIDIsOutcomeUnknown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":true,"message":"SUCCESS","data":{}}`))
	}))
	defer srv.Close()
	_, err := testClient(srv.URL, time.Second).PlaceOrder(map[string]any{})
	if !errors.Is(err, ErrOutcomeUnknown) {
		t.Fatalf("status:true without orderid must be ErrOutcomeUnknown, got %v", err)
	}
}

func TestPlaceOrder_AB1004IsOutcomeUnknown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":false,"message":"Something Went Wrong, Please Try After Sometime","errorcode":"AB1004","data":null}`))
	}))
	defer srv.Close()
	_, err := testClient(srv.URL, time.Second).PlaceOrder(map[string]any{})
	if !errors.Is(err, ErrOutcomeUnknown) {
		t.Fatalf("AB1004 must be ErrOutcomeUnknown, got %v", err)
	}
}

func TestPlaceOrder_JSON5xxIsOutcomeUnknown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusGatewayTimeout)
		_, _ = w.Write([]byte(`{"message":"Endpoint request timed out"}`))
	}))
	defer srv.Close()
	_, err := testClient(srv.URL, time.Second).PlaceOrder(map[string]any{})
	if !errors.Is(err, ErrOutcomeUnknown) {
		t.Fatalf("JSON 5xx must be ErrOutcomeUnknown, got %v", err)
	}
}
