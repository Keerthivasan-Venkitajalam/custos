// Custos ledger gateway — a deterministic state machine.
// It trusts exactly one thing: a valid RS256 signature from the guardrail's
// signing key (Constitution §2). It never inspects prompts, tool-call
// reasoning, or agent confidence. Reject-by-default is the starting state of
// every request handler in this file, not a check bolted on afterward.
package main

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"database/sql"
	_ "embed"
	"encoding/json"
	"encoding/pem"
	"errors"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	_ "github.com/lib/pq"
)

//go:embed migrations/001_init.sql
var migrationSQL string

// runMigrations applies the schema on every boot. Safe to re-run — every
// statement in migrations/001_init.sql is IF-NOT-EXISTS / ON-CONFLICT.
// Exists because Render's managed Postgres has no docker-entrypoint-initdb.d
// equivalent; local dev still gets the schema for free from that instead.
func runMigrations(db *sql.DB) error {
	_, err := db.Exec(migrationSQL)
	return err
}

type debitRequest struct {
	MandateID    string `json:"mandate_id"`
	Amount       string `json:"amount"`
	RecipientVPA string `json:"recipient_vpa"`
}

type debitResponse struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

var (
	pubKey *rsa.PublicKey
	db     *sql.DB

	seenJTIs   = map[string]time.Time{}
	seenJTIsMu sync.Mutex
)

func loadPublicKey(path string) (*rsa.PublicKey, error) {
	var data []byte
	var err error
	// JWT_PUBLIC_KEY_PEM (raw PEM content, e.g. a Render secret) takes
	// priority over a file path — deployed environments shouldn't need a
	// checked-out key file at a predictable relative path.
	if pemEnv := os.Getenv("JWT_PUBLIC_KEY_PEM"); pemEnv != "" {
		data = []byte(pemEnv)
	} else {
		data, err = os.ReadFile(path)
		if err != nil {
			return nil, err
		}
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("invalid PEM public key")
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		return nil, errors.New("not an RSA public key")
	}
	return rsaPub, nil
}

// verifyProof checks the x-custos-proof-of-guardrail JWT. Any failure —
// missing header, bad signature, expired, replayed — returns a reject
// reason and nothing else. There is no code path here that returns "ok"
// without a fully valid signature.
func verifyProof(tokenStr string) (jwt.MapClaims, string, bool) {
	if tokenStr == "" {
		return nil, "REJECTED_UNSIGNED", false
	}

	token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return pubKey, nil
	}, jwt.WithValidMethods([]string{"RS256"}))

	if err != nil || !token.Valid {
		return nil, "REJECTED_INVALID_SIG", false
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, "REJECTED_INVALID_SIG", false
	}

	exp, err := claims.GetExpirationTime()
	if err != nil || exp == nil || time.Now().After(exp.Time) {
		return nil, "REJECTED_EXPIRED", false
	}

	jti, _ := claims["jti"].(string)
	if jti == "" {
		return nil, "REJECTED_INVALID_SIG", false
	}

	seenJTIsMu.Lock()
	// Sweep old entries so the map doesn't grow unbounded.
	for k, t := range seenJTIs {
		if time.Since(t) > 5*time.Minute {
			delete(seenJTIs, k)
		}
	}
	if _, replayed := seenJTIs[jti]; replayed {
		seenJTIsMu.Unlock()
		return nil, "REJECTED_REPLAY", false
	}
	seenJTIs[jti] = time.Now()
	seenJTIsMu.Unlock()

	return claims, "", true
}

func executeDebitHandler(w http.ResponseWriter, r *http.Request) {
	proof := r.Header.Get("x-custos-proof-of-guardrail")

	claims, rejectReason, ok := verifyProof(proof)
	if !ok {
		writeResult(w, http.StatusForbidden, debitResponse{Status: rejectReason, Reason: "signature verification failed"})
		return
	}

	mandateID, _ := claims["mandate_id"].(string)
	amountF, _ := claims["amount"].(float64)
	recipient, _ := claims["recipient_vpa"].(string)
	jti, _ := claims["jti"].(string)

	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	var remaining float64
	err = tx.QueryRowContext(ctx,
		`SELECT total_blocked - utilized FROM mandates WHERE id = $1 FOR UPDATE`, mandateID,
	).Scan(&remaining)
	if err != nil {
		writeResult(w, http.StatusBadRequest, debitResponse{Status: "REJECTED_INVALID_SIG", Reason: "unknown mandate"})
		return
	}

	if amountF > remaining {
		_, _ = tx.ExecContext(ctx,
			`INSERT INTO transactions (mandate_id, amount, recipient_vpa, status, jwt_jti) VALUES ($1,$2,$3,'REJECTED_INSUFFICIENT_FUNDS',$4)`,
			mandateID, amountF, recipient, jti)
		tx.Commit()
		writeResult(w, http.StatusForbidden, debitResponse{Status: "REJECTED_INSUFFICIENT_FUNDS"})
		return
	}

	_, err = tx.ExecContext(ctx, `UPDATE mandates SET utilized = utilized + $1 WHERE id = $2`, amountF, mandateID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO transactions (mandate_id, amount, recipient_vpa, status, jwt_jti) VALUES ($1,$2,$3,'EXECUTED',$4)`,
		mandateID, amountF, recipient, jti)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if err := tx.Commit(); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	log.Printf("EXECUTED debit mandate=%s amount=%.2f recipient=%s jti=%s", mandateID, amountF, recipient, jti)
	writeResult(w, http.StatusOK, debitResponse{Status: "EXECUTED"})
}

func writeResult(w http.ResponseWriter, code int, res debitResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(res)
}

// CORS: this demo deployment is intentionally public read/write on a mock
// gateway with no real money behind it (Constitution §5). Wraps every
// handler so the hosted frontend (a different origin) can reach it.
func withCORS(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, x-custos-proof-of-guardrail")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next(w, r)
	}
}

func mandateStatusHandler(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("mandate_id")
	if id == "" {
		id = "11111111-1111-1111-1111-111111111111"
	}
	var total, utilized float64
	err := db.QueryRow(`SELECT total_blocked, utilized FROM mandates WHERE id = $1`, id).Scan(&total, &utilized)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	json.NewEncoder(w).Encode(map[string]float64{
		"total_blocked": total, "utilized": utilized, "remaining": total - utilized,
	})
}

func main() {
	var err error
	pubKeyPath := os.Getenv("JWT_PUBLIC_KEY_PATH")
	if pubKeyPath == "" {
		pubKeyPath = "../keystore/public_key.pem"
	}
	pubKey, err = loadPublicKey(pubKeyPath)
	if err != nil {
		log.Fatalf("failed to load guardrail public key: %v", err)
	}

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgresql://custos:custos@localhost:5433/custos?sslmode=disable"
	}
	db, err = sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatalf("failed to connect to db: %v", err)
	}
	if err := runMigrations(db); err != nil {
		log.Fatalf("failed to apply migrations: %v", err)
	}

	http.HandleFunc("/execute-debit", withCORS(executeDebitHandler))
	http.HandleFunc("/mandate", withCORS(mandateStatusHandler))

	port := os.Getenv("PORT")
	if port == "" {
		port = "8200"
	}
	log.Printf("Custos gateway listening on :%s — reject-by-default, RS256-only", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
