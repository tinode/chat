package main

import (
	"encoding/json"
	"errors"
	"golang.org/x/net/html"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type linkPreview struct {
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	ImageURL    string `json:"image_url,omitempty"`
}

var client = &http.Client{
	Timeout: time.Second * 2,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if err := validateURL(req.URL.String()); err != nil {
			return err
		}
		return nil
	},
}

// previewLink handles the HTTP request, fetches the URL, and returns the link preview.
func previewLink(w http.ResponseWriter, r *http.Request) {
	// check authorization
	uid, challenge, err := authHttpRequest(r)
	if err != nil {
		http.Error(w, "invalid auth secret", http.StatusBadRequest)
		return
	}
	if challenge != nil {
		http.Error(w, "login challenge not done", http.StatusMultipleChoices)
		return
	}
	if uid.IsZero() {
		http.Error(w, "user not authenticated", http.StatusUnauthorized)
		return
	}

	u := r.URL.Query().Get("url")
	if u == "" {
		http.Error(w, "Missing 'url' query parameter", http.StatusBadRequest)
		return
	}

	if err := validateURL(u); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices { // StatusCode != 20X
		http.Error(w, "Non-OK HTTP status", http.StatusBadGateway)
		return
	}

	body := http.MaxBytesReader(nil, resp.Body, 2*1024) // 2KB limit

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(extractMetadata(body)); err != nil {
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
	}
}

func extractMetadata(body io.Reader) linkPreview {
	var preview linkPreview
	var gotTitle, gotDesc, gotImg, inTitleTag bool

	tokenizer := html.NewTokenizer(body)
	for {
		switch tokenizer.Next() {
		case html.ErrorToken:
			return preview

		case html.StartTagToken, html.SelfClosingTagToken:
			token := tokenizer.Token()
			data := strings.ToLower(token.Data)
			if data == "meta" {
				var name, property, content string
				for _, attr := range token.Attr {
					switch strings.ToLower(attr.Key) {
					case "name":
						name = attr.Val
					case "property":
						property = attr.Val
					case "content":
						content = attr.Val
					}
				}

				if strings.HasPrefix(property, "og:") && content != "" {
					switch property {
					case "og:title":
						preview.Title = content
						gotTitle = true
					case "og:description":
						preview.Description = content
						gotDesc = true
					case "og:image":
						preview.ImageURL = content
						gotImg = true
					}
				} else if name == "description" && preview.Description == "" {
					preview.Description = content
					gotDesc = true
				}
			} else if data == "title" {
				inTitleTag = true
			}

		case html.TextToken:
			if !gotTitle && inTitleTag {
				preview.Title = strings.TrimSpace(tokenizer.Token().Data)
				gotTitle = true
				inTitleTag = false
			}
		}
		if gotTitle && gotDesc && gotImg {
			break
		}
	}
	return preview
}

func validateURL(u string) error {
	parsedURL, err := url.Parse(u)
	if err != nil {
		return err
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return &url.Error{Op: "validate", Err: errors.New("invalid scheme")}
	}

	ips, err := net.LookupIP(parsedURL.Hostname())
	if err != nil {
		return &url.Error{Op: "validate", Err: errors.New("invalid host")}
	}
	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsPrivate() {
			return &url.Error{Op: "validate", Err: errors.New("non routable IP address")}
		}
	}

	return nil
}
