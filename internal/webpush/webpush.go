// Package webpush implements the small subset of RFC 8292 (VAPID) and
// RFC 8291 (aes128gcm content coding) needed to send a Web Push message to a
// browser subscription. It intentionally has no third-party dependency: the
// Go standard library supplies P-256 ECDH/ECDSA/AES-GCM and x/crypto supplies
// HKDF.
package webpush

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/crypto/hkdf"
)

var b64 = base64.RawURLEncoding

type VAPIDKey struct {
	PrivatePEM string `json:"privatePem"`
	PublicKey  string `json:"publicKey"` // base64url, uncompressed P-256
	Subject    string `json:"subject"`
	CreatedAt  string `json:"createdAt"`
}

// GenerateVAPID creates a persistent VAPID identity. The public half is what
// the browser passes to PushManager.subscribe({applicationServerKey}).
func GenerateVAPID(subject string) (*VAPIDKey, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(subject) == "" {
		subject = "mailto:pondera@localhost"
	}
	return &VAPIDKey{
		PrivatePEM: string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})),
		PublicKey:  b64.EncodeToString(elliptic.Marshal(elliptic.P256(), priv.X, priv.Y)),
		Subject:    subject,
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func (k *VAPIDKey) privateKey() (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(k.PrivatePEM))
	if block == nil {
		return nil, errors.New("webpush: invalid VAPID private key PEM")
	}
	return x509.ParseECPrivateKey(block.Bytes)
}

// vapidJWT signs the ES256 authorization token for one audience (the push
// service origin), with a short expiry as required by RFC 8292.
func vapidJWT(k *VAPIDKey, audience string, exp time.Time) (string, error) {
	header := b64.EncodeToString([]byte(`{"typ":"JWT","alg":"ES256"}`))
	claims, err := json.Marshal(map[string]any{
		"aud": audience, "exp": exp.Unix(), "sub": k.Subject,
	})
	if err != nil {
		return "", err
	}
	signingInput := header + "." + b64.EncodeToString(claims)
	digest := sha256.Sum256([]byte(signingInput))
	priv, err := k.privateKey()
	if err != nil {
		return "", err
	}
	r, s, err := ecdsa.Sign(rand.Reader, priv, digest[:])
	if err != nil {
		return "", err
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return signingInput + "." + b64.EncodeToString(sig), nil
}

func audienceOf(endpoint string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("webpush: invalid endpoint %q", endpoint)
	}
	return u.Scheme + "://" + u.Host, nil
}

// Encrypt produces the aes128gcm body for one subscription. p256dh is the
// 65-byte uncompressed client public key and auth is the 16-byte auth secret,
// both base64url-encoded in the browser's PushSubscriptionJSON.
func Encrypt(p256dhB64, authB64 string, payload []byte) ([]byte, error) {
	serverPriv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	return EncryptWith(p256dhB64, authB64, payload, serverPriv, salt)
}

// EncryptWith is the deterministic core of Encrypt. Exposing the ephemeral
// key and salt lets the round-trip test verify the AEAD derivation without
// weakening the production randomness.
func EncryptWith(p256dhB64, authB64 string, payload []byte, serverPriv *ecdh.PrivateKey, salt []byte) ([]byte, error) {
	p256dh, err := b64.DecodeString(p256dhB64)
	if err != nil {
		return nil, fmt.Errorf("webpush: bad p256dh: %w", err)
	}
	auth, err := b64.DecodeString(authB64)
	if err != nil {
		return nil, fmt.Errorf("webpush: bad auth secret: %w", err)
	}
	clientPub, err := ecdh.P256().NewPublicKey(p256dh)
	if err != nil {
		return nil, fmt.Errorf("webpush: bad client public key: %w", err)
	}
	if serverPriv == nil || len(salt) != 16 {
		return nil, errors.New("webpush: EncryptWith requires an ephemeral key and a 16-byte salt")
	}
	shared, err := serverPriv.ECDH(clientPub)
	if err != nil {
		return nil, err
	}
	serverPub := serverPriv.PublicKey().Bytes()

	// RFC 8291 §3.4: PRK_key = HMAC(auth_secret, ECDH_secret)
	prkKey := hmacSHA256(auth, shared)
	info := append([]byte("WebPush: info\x00"), clientPub.Bytes()...)
	info = append(info, serverPub...)
	ikm, err := expand(prkKey, info, 32)
	if err != nil {
		return nil, err
	}
	prk := hmacSHA256(salt, ikm)
	cek, err := expand(prk, []byte("Content-Encoding: aes128gcm\x00"), 16)
	if err != nil {
		return nil, err
	}
	nonce, err := expand(prk, []byte("Content-Encoding: nonce\x00"), 12)
	if err != nil {
		return nil, err
	}
	// Header: salt(16) || rs(4) || idlen(1) || keyid(65)
	header := make([]byte, 0, 86)
	header = append(header, salt...)
	rs := make([]byte, 4)
	binary.BigEndian.PutUint32(rs, 4096)
	header = append(header, rs...)
	header = append(header, byte(len(serverPub)))
	header = append(header, serverPub...)

	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	plaintext := append(append([]byte(nil), payload...), 0x02) // delimiter, no padding
	ciphertext := aead.Seal(nil, nonce, plaintext, header)
	return append(header, ciphertext...), nil
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	_, _ = h.Write(data)
	return h.Sum(nil)
}

func expand(prk, info []byte, n int) ([]byte, error) {
	out := make([]byte, n)
	if _, err := io.ReadFull(hkdf.Expand(sha256.New, prk, info), out); err != nil {
		return nil, err
	}
	return out, nil
}

type Subscription struct {
	Endpoint string `json:"endpoint"`
	P256dh   string `json:"p256dh"`
	Auth     string `json:"auth"`
	Label    string `json:"label,omitempty"`
}

type SendResult struct {
	StatusCode int    `json:"statusCode"`
	Endpoint   string `json:"endpoint"`
	Body       string `json:"body,omitempty"`
}

// Send delivers one notification to a subscription. A 404/410 means the
// subscription expired and the caller should remove it.
func Send(client *http.Client, key *VAPIDKey, sub Subscription, payload []byte, ttl time.Duration) (*SendResult, error) {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	if key == nil || sub.Endpoint == "" || sub.P256dh == "" || sub.Auth == "" {
		return nil, errors.New("webpush: incomplete subscription or VAPID key")
	}
	body, err := Encrypt(sub.P256dh, sub.Auth, payload)
	if err != nil {
		return nil, err
	}
	aud, err := audienceOf(sub.Endpoint)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	jwt, err := vapidJWT(key, aud, now.Add(12*time.Hour))
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, sub.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("TTL", fmt.Sprintf("%d", int(ttl.Seconds())))
	if ttl <= 0 {
		req.Header.Set("TTL", "86400")
	}
	req.Header.Set("Urgency", "high")
	req.Header.Set("Authorization", "vapid t="+jwt+", k="+key.PublicKey)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	return &SendResult{StatusCode: resp.StatusCode, Endpoint: sub.Endpoint, Body: strings.TrimSpace(string(respBody))}, nil
}
