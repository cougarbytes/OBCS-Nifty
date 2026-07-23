package angelone

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/obcs-nifty/backend/internal/config"
)

// Client is a thin wrapper over the AngelOne SmartAPI REST endpoints.
type Client struct {
	cfg  config.AngelOneConfig
	http *http.Client

	mu      sync.RWMutex // guards jwt
	jwt     string
	loginMu sync.Mutex // serializes re-logins across concurrent callers
}

// New creates a client with sane HTTP timeouts.
func New(cfg config.AngelOneConfig) *Client {
	return &Client{
		cfg:  cfg,
		http: &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) headers(jwt string) http.Header {
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	h.Set("Accept", "application/json")
	h.Set("X-UserType", "USER")
	h.Set("X-SourceID", "WEB")
	h.Set("X-ClientLocalIP", nz(c.cfg.LocalIP, "127.0.0.1"))
	h.Set("X-ClientPublicIP", nz(c.cfg.PublicIP, nz(c.cfg.LocalIP, "127.0.0.1")))
	h.Set("X-MACAddress", nz(c.cfg.MACAddr, "00:00:00:00:00:00"))
	h.Set("X-PrivateKey", c.cfg.APIKey)
	if jwt != "" {
		h.Set("Authorization", "Bearer "+jwt)
	}
	return h
}

func (c *Client) token() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.jwt
}

func (c *Client) setToken(jwt string) {
	c.mu.Lock()
	c.jwt = jwt
	c.mu.Unlock()
}

// do sends one request. When withAuth is set and the broker rejects the cached
// session token, it re-logins and replays the request once — AngelOne
// invalidates sessions daily (and on key resets or logins elsewhere), and this
// process is long-lived.
func (c *Client) do(ctx context.Context, method, path string, body any, withAuth bool) (map[string]any, error) {
	var payload []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		payload = b
	}
	jwt := ""
	if withAuth {
		jwt = c.token()
	}
	res, err := c.doOnce(ctx, method, path, payload, jwt)
	if err != nil || !withAuth || !isSessionRejected(res) {
		return res, err
	}
	if err := c.relogin(ctx, jwt); err != nil {
		return nil, fmt.Errorf("re-login after token rejection: %w", err)
	}
	return c.doOnce(ctx, method, path, payload, c.token())
}

func (c *Client) doOnce(ctx context.Context, method, path string, payload []byte, jwt string) (map[string]any, error) {
	var reader io.Reader
	if payload != nil {
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.cfg.BaseURL+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header = c.headers(jwt)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		// A non-JSON body is a gateway/WAF response (e.g. a 403 "Access Denied"
		// page), not an application-level error. Surface the HTTP status and a
		// body snippet so the real cause is visible instead of a cryptic
		// "invalid character" decode error.
		snippet := strings.TrimSpace(string(raw))
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return nil, fmt.Errorf("http %d from %s: non-JSON response: %s", resp.StatusCode, path, snippet)
	}
	return out, nil
}

// isSessionRejected reports whether the broker refused the session token
// (daily expiry, key reset, login elsewhere) as opposed to a request-level
// failure. SmartAPI signals this with AG8001/AG8002/AG8003.
func isSessionRejected(res map[string]any) bool {
	if ok, _ := res["status"].(bool); ok {
		return false
	}
	code, _ := res["errorcode"].(string)
	switch code {
	case "AG8001", "AG8002", "AG8003": // invalid / expired / missing token
		return true
	}
	msg, _ := res["message"].(string)
	msg = strings.ToLower(msg)
	return strings.Contains(msg, "invalid token") || strings.Contains(msg, "token expired")
}

// relogin refreshes the session at most once per stale token: concurrent
// callers that raced on the same expired jwt queue on loginMu, then find the
// token already replaced and skip their own login attempt.
func (c *Client) relogin(ctx context.Context, stale string) error {
	c.loginMu.Lock()
	defer c.loginMu.Unlock()
	if c.token() != stale {
		return nil
	}
	return c.Login(ctx)
}

// Login authenticates via password (MPIN) + TOTP and caches the JWT.
// Ported from simulator._ao_login.
func (c *Client) Login(ctx context.Context) error {
	totp, err := totpNow(c.cfg.TOTPSecret)
	if err != nil {
		return err
	}
	body := map[string]string{
		"clientcode": c.cfg.ClientCode,
		"password":   c.cfg.MPIN,
		"totp":       totp,
	}
	res, err := c.do(ctx, http.MethodPost,
		"/rest/auth/angelbroking/user/v1/loginByPassword", body, false)
	if err != nil {
		return err
	}
	if ok, _ := res["status"].(bool); !ok {
		return fmt.Errorf("angelone login failed: %v", res["message"])
	}
	data, _ := res["data"].(map[string]any)
	jwt, _ := data["jwtToken"].(string)
	if jwt == "" {
		return fmt.Errorf("angelone login: empty jwt")
	}
	c.setToken(jwt)
	return nil
}

// LTP fetches the last traded price for a symbol token on an exchange segment.
func (c *Client) LTP(ctx context.Context, exchange, tradingSymbol, symbolToken string) (float64, error) {
	body := map[string]string{
		"exchange":      exchange,
		"tradingsymbol": tradingSymbol,
		"symboltoken":   symbolToken,
	}
	res, err := c.do(ctx, http.MethodPost,
		"/rest/secure/angelbroking/order/v1/getLtpData", body, true)
	if err != nil {
		return 0, err
	}
	if ok, _ := res["status"].(bool); !ok {
		return 0, fmt.Errorf("ltp failed: %v", res["message"])
	}
	data, _ := res["data"].(map[string]any)
	return toFloat(data["ltp"]), nil
}

// Candle is one OHLCV bar.
type Candle struct {
	Time                   time.Time
	Open, High, Low, Close float64
	Volume                 int64
}

// History fetches daily candles for a token. Ported from simulator._ao_history.
func (c *Client) History(ctx context.Context, exchange, symbolToken, fromDate, toDate string) ([]Candle, error) {
	body := map[string]string{
		"exchange":    exchange,
		"symboltoken": symbolToken,
		"interval":    "ONE_DAY",
		"fromdate":    fromDate,
		"todate":      toDate,
	}
	res, err := c.do(ctx, http.MethodPost,
		"/rest/secure/angelbroking/historical/v1/getCandleData", body, true)
	if err != nil {
		return nil, err
	}
	if ok, _ := res["status"].(bool); !ok {
		return nil, fmt.Errorf("history failed: %v", res["message"])
	}
	rows, _ := res["data"].([]any)
	out := make([]Candle, 0, len(rows))
	for _, r := range rows {
		arr, ok := r.([]any)
		if !ok || len(arr) < 6 {
			continue
		}
		ts, _ := arr[0].(string)
		t, _ := time.Parse(time.RFC3339, ts)
		out = append(out, Candle{
			Time:   t,
			Open:   toFloat(arr[1]),
			High:   toFloat(arr[2]),
			Low:    toFloat(arr[3]),
			Close:  toFloat(arr[4]),
			Volume: int64(toFloat(arr[5])),
		})
	}
	return out, nil
}

// OrderRequest is a single-leg order.
type OrderRequest struct {
	TradingSymbol   string
	SymbolToken     string
	TransactionType string // BUY | SELL
	Exchange        string // NFO
	Quantity        int
	ProductType     string // CARRYFORWARD for overnight
	OrderTag        string // groups the legs of one spread (optional)
}

// OrderAck is the pair of identifiers returned when an order is accepted. The
// orderid is the broker-visible reference; the uniqueorderid is what the order
// status API is keyed on.
type OrderAck struct {
	OrderID       string
	UniqueOrderID string
}

// PlaceOrder submits a market order and returns the broker order identifiers.
func (c *Client) PlaceOrder(ctx context.Context, o OrderRequest) (OrderAck, error) {
	body := map[string]string{
		"variety":         "NORMAL",
		"tradingsymbol":   o.TradingSymbol,
		"symboltoken":     o.SymbolToken,
		"transactiontype": o.TransactionType,
		"exchange":        o.Exchange,
		"ordertype":       "MARKET",
		"producttype":     nz(o.ProductType, "CARRYFORWARD"),
		"duration":        "DAY",
		"quantity":        strconv.Itoa(o.Quantity),
	}
	if o.OrderTag != "" {
		body["ordertag"] = o.OrderTag
	}
	res, err := c.do(ctx, http.MethodPost,
		"/rest/secure/angelbroking/order/v1/placeOrder", body, true)
	if err != nil {
		return OrderAck{}, err
	}
	if ok, _ := res["status"].(bool); !ok {
		return OrderAck{}, fmt.Errorf("place order failed: %v", res["message"])
	}
	data, _ := res["data"].(map[string]any)
	id, _ := data["orderid"].(string)
	uid, _ := data["uniqueorderid"].(string)
	return OrderAck{OrderID: id, UniqueOrderID: uid}, nil
}

// OrderDetail is the settled state of one order from the individual order
// status API, keyed on the uniqueorderid returned by PlaceOrder.
type OrderDetail struct {
	Status       string  // "complete" | "rejected" | "cancelled" | "open" | ...
	AveragePrice float64 // true average fill price (points)
	FilledShares int
}

// OrderStatus fetches the settled state of a single order. Use AveragePrice for
// the true fill instead of approximating with a post-order LTP.
func (c *Client) OrderStatus(ctx context.Context, uniqueOrderID string) (OrderDetail, error) {
	res, err := c.do(ctx, http.MethodGet,
		"/rest/secure/angelbroking/order/v1/details/"+uniqueOrderID, nil, true)
	if err != nil {
		return OrderDetail{}, err
	}
	if ok, _ := res["status"].(bool); !ok {
		return OrderDetail{}, fmt.Errorf("order status failed: %v", res["message"])
	}
	data, _ := res["data"].(map[string]any)
	status, _ := data["status"].(string)
	if status == "" {
		status, _ = data["orderstatus"].(string)
	}
	return OrderDetail{
		Status:       status,
		AveragePrice: toFloat(data["averageprice"]),
		FilledShares: int(toFloat(data["filledshares"])),
	}, nil
}

// Funds is the account's RMS (Risk Management System) funds/margin snapshot.
type Funds struct {
	Net                    float64 // total net available funds
	AvailableCash          float64 // free cash usable for new trades
	AvailableIntradayPayin float64
	Collateral             float64
	UtilisedDebits         float64
}

// Funds fetches the live RMS limit (funds & margin) for the account. This is the
// authoritative source of the account's tradable equity in live mode.
func (c *Client) Funds(ctx context.Context) (Funds, error) {
	res, err := c.do(ctx, http.MethodGet,
		"/rest/secure/angelbroking/user/v1/getRMS", nil, true)
	if err != nil {
		return Funds{}, err
	}
	if ok, _ := res["status"].(bool); !ok {
		return Funds{}, fmt.Errorf("funds fetch failed: %v", res["message"])
	}
	data, _ := res["data"].(map[string]any)
	return Funds{
		Net:                    toFloat(data["net"]),
		AvailableCash:          toFloat(data["availablecash"]),
		AvailableIntradayPayin: toFloat(data["availableintradaypayin"]),
		Collateral:             toFloat(data["collateral"]),
		UtilisedDebits:         toFloat(data["utiliseddebits"]),
	}, nil
}

// MarginPosition is one leg passed to the margin calculator batch API. Qty is in
// units (lots * lot_size), not number of lots.
type MarginPosition struct {
	Exchange    string  // NFO
	SymbolToken string  // scrip token
	Qty         int     // units
	TradeType   string  // BUY | SELL
	ProductType string  // CARRYFORWARD | INTRADAY | ...
	Price       float64 // 0 for a market order estimate
}

// MarginResult is the batch margin calculator response. TotalMarginRequired is
// the net requirement for the whole basket after MarginBenefit (the reduction a
// hedged position such as a spread earns).
type MarginResult struct {
	TotalMarginRequired float64
	NetPremium          float64
	SpanMargin          float64
	MarginBenefit       float64
}

// RequiredMargin asks the broker to price the margin for a basket of positions.
// Passing both spread legs together yields the true hedged requirement (a naked
// short leg on its own would be far larger).
func (c *Client) RequiredMargin(ctx context.Context, positions []MarginPosition) (MarginResult, error) {
	pos := make([]map[string]any, 0, len(positions))
	for _, p := range positions {
		pos = append(pos, map[string]any{
			"exchange":    p.Exchange,
			"qty":         p.Qty,
			"price":       p.Price,
			"productType": nz(p.ProductType, "CARRYFORWARD"),
			"token":       p.SymbolToken,
			"tradeType":   p.TradeType,
			"orderType":   "MARKET", // required by the batch API; legs are market orders
		})
	}
	res, err := c.do(ctx, http.MethodPost,
		"/rest/secure/angelbroking/margin/v1/batch", map[string]any{"positions": pos}, true)
	if err != nil {
		return MarginResult{}, err
	}
	if ok, _ := res["status"].(bool); !ok {
		return MarginResult{}, fmt.Errorf("margin calc failed: %v", res["message"])
	}
	data, _ := res["data"].(map[string]any)
	comp, _ := data["marginComponents"].(map[string]any)
	return MarginResult{
		TotalMarginRequired: toFloat(data["totalMarginRequired"]),
		NetPremium:          toFloat(comp["netPremium"]),
		SpanMargin:          toFloat(comp["spanMargin"]),
		MarginBenefit:       toFloat(comp["marginBenefit"]),
	}, nil
}

func nz(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func toFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case string:
		f, _ := strconv.ParseFloat(x, 64)
		return f
	case int:
		return float64(x)
	default:
		return 0
	}
}
