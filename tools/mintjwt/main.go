// mintjwt 是本地联调工具：起一个假 OIDC 签发者（/.well-known/jwks），
// 并按常用变体铸造一组 JWT 写到输出文件，供 curl 手测 /v1/export 全链路。
//
// 用法：
//   go run ./tools/mintjwt -issuer http://127.0.0.1:8899 -aud https://secrets.test -out data/tokens.json
//
// 产出的 tokens.json：{ "valid": "...", "wrong_owner": "...", "wrong_aud": "...", "expired": "..." }
package main

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"os/signal"
	"time"
)

func main() {
	issuer := flag.String("issuer", "http://127.0.0.1:8899", "本工具对外暴露的签发者地址（实例配置里填它）")
	addr := flag.String("addr", "127.0.0.1:8899", "监听地址")
	aud := flag.String("aud", "https://secrets.test", "目标 audience")
	owner := flag.String("owner", "lyzz0612", "repository_owner 值")
	repo := flag.String("repo", "lyzz0612/minigames", "repository 值")
	repoID := flag.Int("repo-id", 987654321, "repository_id 值")
	out := flag.String("out", "data/tokens.json", "token 输出文件")
	flag.Parse()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		fatal(err)
	}
	kid := "mintjwt-key-1"

	jwks := map[string]any{
		"keys": []map[string]any{{
			"kty": "RSA", "kid": kid, "alg": "RS256", "use": "sig",
			"n":   b64(key.N.Bytes()),
			"e":   b64(big.NewInt(int64(key.E)).Bytes()),
		}},
	}
	http.HandleFunc("/.well-known/jwks", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(jwks)
	})

	base := func() map[string]any {
		return map[string]any{
			"iss": *issuer, "aud": *aud,
			"iat": time.Now().Unix(),
			"sub": "repo:" + *repo + ":ref:refs/heads/main",
			"repository":        *repo,
			"repository_id":     *repoID,
			"repository_owner":  *owner,
			"event_name":        "push",
			"ref":               "refs/heads/main",
			"actor":             *owner,
		}
	}

	mint := func(mutate func(map[string]any)) string {
		c := base()
		c["exp"] = time.Now().Add(5 * time.Minute).Unix()
		mutate(c)
		return sign(key, kid, c)
	}

	tokens := map[string]string{
		"valid":       mint(func(c map[string]any) {}),
		"wrong_owner": mint(func(c map[string]any) { c["repository_owner"] = "someone-else" }),
		"wrong_aud":   mint(func(c map[string]any) { c["aud"] = "https://other-service" }),
		"expired":     mint(func(c map[string]any) { c["exp"] = time.Now().Add(-time.Hour).Unix() }),
		"pr_event":    mint(func(c map[string]any) { c["event_name"] = "pull_request" }),
	}

	if err := os.MkdirAll(dirOf(*out), 0o755); err != nil {
		fatal(err)
	}
	b, _ := json.MarshalIndent(tokens, "", "  ")
	if err := os.WriteFile(*out, b, 0o644); err != nil {
		fatal(err)
	}

	go func() {
		fmt.Printf("假 OIDC 签发者: http://%s/.well-known/jwks\n", *addr)
		fmt.Printf("token 已写入: %s\n", *out)
		fmt.Printf("实例配置建议: {\"audience\": %q, \"issuer\": %q}\n", *aud, *issuer)
	}()
	srv := &http.Server{Addr: *addr}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	go func() {
		<-sig
		srv.Close()
	}()
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		fatal(err)
	}
}

func dirOf(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' || p[i] == '\\' {
			return p[:i]
		}
	}
	return "."
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func sign(key *rsa.PrivateKey, kid string, claims map[string]any) string {
	hb, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": kid})
	cb, _ := json.Marshal(claims)
	input := b64(hb) + "." + b64(cb)
	digest := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		fatal(err)
	}
	return input + "." + b64(sig)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "mintjwt:", err)
	os.Exit(1)
}
