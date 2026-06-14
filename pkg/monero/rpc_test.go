package monero

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWalletClientDigestAuth(t *testing.T) {
	const (
		user  = "xns"
		pass  = "secret"
		realm = "monero-rpc"
		nonce = "abcdef"
	)
	var authed bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth == "" {
			w.Header().Set("WWW-Authenticate", `Digest realm="`+realm+`", nonce="`+nonce+`", qop="auth"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		for _, want := range []string{
			`Digest username="` + user + `"`,
			`realm="` + realm + `"`,
			`nonce="` + nonce + `"`,
			`uri="/json_rpc"`,
			`qop=auth`,
		} {
			if !strings.Contains(auth, want) {
				t.Fatalf("authorization header %q does not contain %q", auth, want)
			}
		}
		authed = true
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      "0",
			"result":  map[string]any{},
		})
	}))
	defer srv.Close()

	wallet := NewWalletWithLogin(srv.URL, user, pass)
	if err := wallet.Call("get_version", map[string]any{}, nil); err != nil {
		t.Fatal(err)
	}
	if !authed {
		t.Fatal("server never received authenticated request")
	}
}

func TestDigestAuthRejectsUnsupportedAlgorithm(t *testing.T) {
	auth := &digestAuth{Username: "xns", Password: "secret"}
	_, err := auth.authorization("POST", "http://127.0.0.1:18083/json_rpc", `Digest realm="monero-rpc", nonce="abcdef", qop="auth", algorithm="SHA-256"`)
	if err == nil || !strings.Contains(err.Error(), "unsupported digest algorithm") {
		t.Fatalf("expected unsupported algorithm error, got %v", err)
	}
}
