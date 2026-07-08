package angelone

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/obcs-nifty/backend/internal/config"
)

// Client is a thin wrapper over the AngelOne SmartAPI REST endpoints.
type Client struct {
	cfg  config.AngelOneConfig
	http *http.Client
	jwt  string
}

// New creates a client with sane HTTP timeouts.
func New(cfg config.AngelOneConfig) *Client {
	return &Client{
		cfg:  cfg,
		http: &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) headers(withAuth bool) http.Header {
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	h.Set("Accept", "application/json")
	h.Set("X-UserType", "USER")
	h.Set("X-SourceID", "WEB")
	h.Set("X-ClientLocalIP", nz(c.cfg.LocalIP, "127.0.0.1"))
	h.Set("X-ClientPublicIP", nz(c.cfg.PublicIP, nz(c.cfg.LocalIP, "127.0.0.1")))
	h.Set("X-MACAddress", nz(c.cfg.MACAddr, "00:00:00:00:00:00"))
	h.Set("X-PrivateKey", c.cfg.APIKey)
	if withAuth && c.jwt != "" {
		h.Set("Authorization", "Bearer "+c.jwt)
	}
	return h
}

func (c *Client) do(ctx context.Context, method, path string, body any, withAuth bool) (map[string]any, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.cfg.BaseURL+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header = c.headers(withAuth)

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
		return nil, fmt.Errorf("decode response (%d): %w", resp.StatusCode, err)
	}
	return out, nil
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
	c.jwt = jwt
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
}

// PlaceOrder submits a market order and returns the broker order id.
func (c *Client) PlaceOrder(ctx context.Context, o OrderRequest) (string, error) {
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
	res, err := c.do(ctx, http.MethodPost,
		"/rest/secure/angelbroking/order/v1/placeOrder", body, true)
	if err != nil {
		return "", err
	}
	if ok, _ := res["status"].(bool); !ok {
		return "", fmt.Errorf("place order failed: %v", res["message"])
	}
	data, _ := res["data"].(map[string]any)
	id, _ := data["orderid"].(string)
	return id, nil
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
