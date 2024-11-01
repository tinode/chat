package main

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v4"
)

type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	ExpiresIn    int64  `json:"expires_in"`
	IDToken      string `json:"id_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
}

func main() {
	http.HandleFunc("/auth/token", token)

	port := "8080"
	fmt.Printf("Mock Apple server running on http://localhost:%s\n", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

func token(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	clientID := r.FormValue("client_id")
	clientSecret := r.FormValue("client_secret")
	code := r.FormValue("code")
	grantType := r.FormValue("grant_type")

	if clientID == "" || clientSecret == "" || code == "" || grantType != "authorization_code" {
		http.Error(w, "Invalid request parameters", http.StatusBadRequest)
		return
	}

	idToken, err := generateIDToken(clientID, "mockuser")
	if err != nil {
		http.Error(w, "Failed to generate ID token", http.StatusInternalServerError)
		return
	}

	response := TokenResponse{
		AccessToken:  "mock_access_token",
		ExpiresIn:    3600,
		IDToken:      idToken,
		RefreshToken: "mock_refresh_token",
		TokenType:    "Bearer",
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func generateIDToken(clientID, userID string) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iss":            "https://appleid.apple.com",
		"aud":            clientID,
		"sub":            userID,
		"iat":            time.Now().Unix(),
		"exp":            time.Now().Add(1 * time.Hour).Unix(),
		"email":          "mockuser@example.com",
		"email_verified": true,
	})

	keyByte, err := os.ReadFile("private_key.p8")
	if err != nil {
		return "", errors.New("failed to read key: " + err.Error())
	}

	block, _ := pem.Decode(keyByte)
	key, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		return "", errors.New("failed to parse key: " + err.Error())
	}

	tokenString, err := token.SignedString(key)
	if err != nil {
		return "", fmt.Errorf("error signing IDToken: %v", err)
	}

	return tokenString, nil
}
