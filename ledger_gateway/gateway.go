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
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
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

func mandateStatusHandler(w http.ResponseWriter, r *http.Request) {
	// CORS: local demo only, so the browser-based X-Ray UI can read balances
	// directly. Never appropriate outside a local dev/demo box.
	w.Header().Set("Access-Control-Allow-Origin", "*")
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

	http.HandleFunc("/execute-debit", executeDebitHandler)
	http.HandleFunc("/mandate", mandateStatusHandler)

	log.Println("Custos gateway listening on :8200 — reject-by-default, RS256-only")
	log.Fatal(http.ListenAndServe(":8200", nil))
}
