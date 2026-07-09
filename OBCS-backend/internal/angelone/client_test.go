package angelone

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/obcs-nifty/backend/internal/config"
)

// newTestClient wires a client whose requests hit the given handler.
func newTestClient(h http.Handler) (*Client, *httptest.Server) {
	srv := httptest.NewServer(h)
	c := New(config.AngelOneConfig{BaseURL: srv.URL, APIKey: "test-key"})
	c.jwt = "test-jwt"
	return c, srv
}

func TestFundsParsesRMSStrings(t *testing.T) {
	// getRMS returns numeric fields as strings; toFloat must parse them.
	c, srv := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/user/v1/getRMS") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		io.WriteString(w, `{"status":true,"message":"SUCCESS","data":{
			"net":"152340.55","availablecash":"148000.00","collateral":"0",
			"utiliseddebits":"4340.55"}}`)
	}))
	defer srv.Close()

	f, err := c.Funds(context.Background())
	if err != nil {
		t.Fatalf("Funds: %v", err)
	}
	if f.AvailableCash != 148000.00 {
		t.Errorf("AvailableCash = %v, want 148000", f.AvailableCash)
	}
	if f.Net != 152340.55 {
		t.Errorf("Net = %v, want 152340.55", f.Net)
	}
}

func TestFundsErrorsOnStatusFalse(t *testing.T) {
	c, srv := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"status":false,"message":"Invalid Token","data":null}`)
	}))
	defer srv.Close()
	if _, err := c.Funds(context.Background()); err == nil {
		t.Fatal("expected error on status=false, got nil")
	}
}

func TestRequiredMarginSendsBasketAndParses(t *testing.T) {
	var gotPositions int
	var gotToken, gotTradeType string
	c, srv := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/margin/v1/batch") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		var body struct {
			Positions []map[string]any `json:"positions"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		gotPositions = len(body.Positions)
		if len(body.Positions) > 1 {
			gotToken, _ = body.Positions[1]["token"].(string)
			gotTradeType, _ = body.Positions[1]["tradeType"].(string)
		}
		io.WriteString(w, `{"status":true,"message":"SUCCESS","data":{
			"totalMarginRequired":29612.35,
			"marginComponents":{"netPremium":5060.0,"spanMargin":0.0,"marginBenefit":79876.5}}}`)
	}))
	defer srv.Close()

	res, err := c.RequiredMargin(context.Background(), []MarginPosition{
		{Exchange: "NFO", SymbolToken: "67300", Qty: 50, TradeType: "BUY", ProductType: "CARRYFORWARD"},
		{Exchange: "NFO", SymbolToken: "67308", Qty: 50, TradeType: "SELL", ProductType: "CARRYFORWARD"},
	})
	if err != nil {
		t.Fatalf("RequiredMargin: %v", err)
	}
	if gotPositions != 2 {
		t.Errorf("sent %d positions, want 2", gotPositions)
	}
	if gotToken != "67308" || gotTradeType != "SELL" {
		t.Errorf("short leg = token %q %q, want 67308 SELL", gotToken, gotTradeType)
	}
	if res.TotalMarginRequired != 29612.35 {
		t.Errorf("TotalMarginRequired = %v, want 29612.35", res.TotalMarginRequired)
	}
	if res.MarginBenefit != 79876.5 {
		t.Errorf("MarginBenefit = %v, want 79876.5", res.MarginBenefit)
	}
}

func TestOrderStatusParsesAverageFill(t *testing.T) {
	c, srv := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/order/v1/details/") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		io.WriteString(w, `{"status":true,"message":"SUCCESS","data":{
			"status":"complete","averageprice":123.45,"filledshares":"65"}}`)
	}))
	defer srv.Close()

	d, err := c.OrderStatus(context.Background(), "uid-1")
	if err != nil {
		t.Fatalf("OrderStatus: %v", err)
	}
	if d.Status != "complete" || d.AveragePrice != 123.45 || d.FilledShares != 65 {
		t.Errorf("got %+v, want complete/123.45/65", d)
	}
}

func TestPlaceOrderTagsAndReturnsAck(t *testing.T) {
	var gotTag string
	c, srv := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		gotTag = body["ordertag"]
		io.WriteString(w, `{"status":true,"message":"SUCCESS","data":{
			"orderid":"241010000012345","uniqueorderid":"abc-uid"}}`)
	}))
	defer srv.Close()

	ack, err := c.PlaceOrder(context.Background(), OrderRequest{
		TradingSymbol: "NIFTY25000CE", SymbolToken: "67300",
		TransactionType: "BUY", Exchange: "NFO", Quantity: 65,
		ProductType: "CARRYFORWARD", OrderTag: "OBCS123",
	})
	if err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}
	if gotTag != "OBCS123" {
		t.Errorf("ordertag = %q, want OBCS123", gotTag)
	}
	if ack.OrderID != "241010000012345" || ack.UniqueOrderID != "abc-uid" {
		t.Errorf("ack = %+v, want orderid/uniqueorderid populated", ack)
	}
}
