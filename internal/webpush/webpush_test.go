package webpush

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	clientPriv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serverPriv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := make([]byte, 16)
	if _, err := rand.Read(auth); err != nil {
		t.Fatal(err)
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"title":"任务完成","body":"测试通过"}`)
	body, err := EncryptWith(b64.EncodeToString(clientPriv.PublicKey().Bytes()), b64.EncodeToString(auth), payload, serverPriv, salt)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) < 86+len(payload)+17 {
		t.Fatalf("encrypted body too short: %d", len(body))
	}
	if got := binary.BigEndian.Uint32(body[16:20]); got != 4096 {
		t.Fatalf("record size = %d, want 4096", got)
	}
	if int(body[20]) != 65 {
		t.Fatalf("keyid length = %d, want 65", body[20])
	}
	header := body[:86]
	serverPubBytes := body[21:86]
	serverPub, err := ecdh.P256().NewPublicKey(serverPubBytes)
	if err != nil {
		t.Fatal(err)
	}
	shared, err := clientPriv.ECDH(serverPub)
	if err != nil {
		t.Fatal(err)
	}
	prkKey := hmacSHA256(auth, shared)
	info := append([]byte("WebPush: info\x00"), clientPriv.PublicKey().Bytes()...)
	info = append(info, serverPubBytes...)
	ikm, err := expand(prkKey, info, 32)
	if err != nil {
		t.Fatal(err)
	}
	prk := hmacSHA256(salt, ikm)
	cek, err := expand(prk, []byte("Content-Encoding: aes128gcm\x00"), 16)
	if err != nil {
		t.Fatal(err)
	}
	nonce, err := expand(prk, []byte("Content-Encoding: nonce\x00"), 12)
	if err != nil {
		t.Fatal(err)
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		t.Fatal(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := aead.Open(nil, nonce, body[86:], header)
	if err != nil {
		t.Fatalf("AES-GCM open failed (key derivation mismatch): %v", err)
	}
	if len(plain) == 0 || plain[len(plain)-1] != 0x02 {
		t.Fatalf("missing padding delimiter: %v", plain)
	}
	if !bytes.Equal(plain[:len(plain)-1], payload) {
		t.Fatalf("decrypted payload mismatch: %s", plain)
	}
}

func TestVAPIDKeyAndJWT(t *testing.T) {
	k, err := GenerateVAPID("mailto:me@example.com")
	if err != nil {
		t.Fatal(err)
	}
	pubBytes, err := b64.DecodeString(k.PublicKey)
	if err != nil || len(pubBytes) != 65 {
		t.Fatalf("bad public key: %v %d", err, len(pubBytes))
	}
	token, err := vapidJWT(k, "https://fcm.googleapis.com", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatal("JWT must have three segments")
	}
	var claims map[string]any
	raw, err := b64.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &claims); err != nil {
		t.Fatal(err)
	}
	if claims["aud"] != "https://fcm.googleapis.com" || claims["sub"] != "mailto:me@example.com" {
		t.Fatalf("bad claims: %v", claims)
	}
	x, y := elliptic.Unmarshal(elliptic.P256(), pubBytes)
	if x == nil {
		t.Fatal("public key is not on P-256")
	}
	sig, err := b64.DecodeString(parts[2])
	if err != nil || len(sig) != 64 {
		t.Fatalf("bad ES256 signature: %v %d", err, len(sig))
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if !ecdsa.Verify(&ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}, digest[:],
		new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		t.Fatal("VAPID signature does not verify")
	}
}

func TestSendBuildsValidPushRequest(t *testing.T) {
	clientPriv, _ := ecdh.P256().GenerateKey(rand.Reader)
	auth := make([]byte, 16)
	_, _ = rand.Read(auth)
	k, err := GenerateVAPID("")
	if err != nil {
		t.Fatal(err)
	}
	var gotHeaders http.Header
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		gotBody, _ = readAll(r)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	res, err := Send(srv.Client(), k, Subscription{
		Endpoint: srv.URL + "/push/abc",
		P256dh:   b64.EncodeToString(clientPriv.PublicKey().Bytes()),
		Auth:     b64.EncodeToString(auth),
	}, []byte(`{"title":"hi"}`), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", res.StatusCode)
	}
	if gotHeaders.Get("Content-Encoding") != "aes128gcm" || !strings.HasPrefix(gotHeaders.Get("Authorization"), "vapid t=") {
		t.Fatalf("missing push headers: %v", gotHeaders)
	}
	if len(gotBody) < 86 {
		t.Fatal("push body is not aes128gcm encoded")
	}
}

func TestAudienceAndBadSubscription(t *testing.T) {
	if _, err := audienceOf("not-a-url"); err == nil {
		t.Fatal("expected invalid endpoint to be rejected")
	}
	if aud, err := audienceOf("https://push.example.com/x/y"); err != nil || aud != "https://push.example.com" {
		t.Fatalf("audience = %q, %v", aud, err)
	}
	if _, err := Send(nil, nil, Subscription{}, nil, 0); err == nil {
		t.Fatal("incomplete subscription must be refused")
	}
}

func readAll(r *http.Request) ([]byte, error) {
	var buf bytes.Buffer
	_, err := buf.ReadFrom(r.Body)
	return buf.Bytes(), err
}

var _ = base64.RawURLEncoding
