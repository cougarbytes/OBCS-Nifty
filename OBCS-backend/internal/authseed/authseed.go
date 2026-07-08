// Package authseed provisions the single application user in Supabase Auth on
// first boot. It generates a random two-word username and a 32-character
// cryptographically-random password, creates the user via the GoTrue admin API
// and prints the credentials exactly once. Credentials are never written to
// disk or committed (per spec + OWASP A02/A07).
package authseed

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/obcs-nifty/backend/internal/db"
)

// passwordAlphabet excludes ambiguous/shell-sensitive characters while keeping
// ~190 bits of entropy over 32 chars.
const passwordAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"

var adjectives = []string{
	"amber", "brave", "clever", "dapper", "eager", "fabled", "golden", "hidden",
	"iron", "jolly", "keen", "lucky", "mellow", "noble", "olive", "prime",
	"quiet", "rapid", "silver", "trusty", "umber", "vivid", "witty", "zesty",
}

var nouns = []string{
	"tiger", "falcon", "otter", "cobra", "lynx", "raven", "panther", "bison",
	"heron", "jaguar", "kestrel", "mamba", "nimbus", "osprey", "puma", "quokka",
	"rhino", "stallion", "tapir", "urchin", "viper", "walrus", "yak", "zebra",
}

// Credentials is the generated app login.
type Credentials struct {
	Username string
	Email    string
	Password string
}

// randInt returns a uniform int in [0,n) using crypto/rand.
func randInt(n int) int {
	v, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		panic(fmt.Sprintf("crypto/rand failure: %v", err))
	}
	return int(v.Int64())
}

// Generate builds a random two-word username and a 32-char password. The login
// email is derived as <username>@obcs.local (Supabase Auth is email-based).
func Generate() Credentials {
	username := fmt.Sprintf("%s-%s", adjectives[randInt(len(adjectives))], nouns[randInt(len(nouns))])
	var sb strings.Builder
	for i := 0; i < 32; i++ {
		sb.WriteByte(passwordAlphabet[randInt(len(passwordAlphabet))])
	}
	return Credentials{
		Username: username,
		Email:    username + "@obcs.local",
		Password: sb.String(),
	}
}

// EnsureUser provisions the app user once. It is a no-op if the bootstrap marker
// is already set. Requires the Supabase URL and service-role key.
func EnsureUser(ctx context.Context, store *db.Store, supabaseURL, serviceKey string, log *slog.Logger) error {
	seeded, err := store.IsSeeded(ctx)
	if err != nil {
		return fmt.Errorf("check bootstrap: %w", err)
	}
	if seeded {
		log.Info("app user already provisioned; skipping seed")
		return nil
	}
	if supabaseURL == "" || serviceKey == "" {
		log.Warn("SUPABASE_URL or service key missing; skipping user seed")
		return nil
	}

	creds := Generate()
	userID, err := createGoTrueUser(ctx, supabaseURL, serviceKey, creds)
	if err != nil {
		return fmt.Errorf("create auth user: %w", err)
	}
	if err := store.MarkSeeded(ctx, creds.Username, userID); err != nil {
		return fmt.Errorf("mark seeded: %w", err)
	}

	printCredentials(creds)
	return nil
}

func createGoTrueUser(ctx context.Context, supabaseURL, serviceKey string, c Credentials) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"email":         c.Email,
		"password":      c.Password,
		"email_confirm": true,
		"user_metadata": map[string]string{"username": c.Username},
	})
	url := strings.TrimRight(supabaseURL, "/") + "/auth/v1/admin/users"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("apikey", serviceKey)
	req.Header.Set("Authorization", "Bearer "+serviceKey)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("gotrue admin create returned %d: %s", resp.StatusCode, string(raw))
	}
	var out struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(raw, &out)
	return out.ID, nil
}

func printCredentials(c Credentials) {
	line := strings.Repeat("=", 64)
	fmt.Printf("\n%s\n", line)
	fmt.Println("  OBCS-Nifty — application login (shown ONCE, not stored):")
	fmt.Printf("    Username : %s\n", c.Username)
	fmt.Printf("    Email    : %s   (use this to sign in)\n", c.Email)
	fmt.Printf("    Password : %s\n", c.Password)
	fmt.Println("  Save these now. The password is not recoverable.")
	fmt.Printf("%s\n\n", line)
}
