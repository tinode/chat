package main

import (
	"encoding/json"
	"errors"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

type linkPreview struct {
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	ImageURL    string `json:"image_url,omitempty"`
}

var client = &http.Client{
	Timeout: time.Second * 2,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if err := validateURL(req.URL); err != nil {
			return err
		}
		return nil
	},
}

// previewLink handles the HTTP request, fetches the URL, and returns the link preview.
func previewLink(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// check authorization
	uid, challenge, err := authHttpRequest(r)
	if err != nil {
		http.Error(w, "invalid auth secret", http.StatusBadRequest)
		return
	}
	if challenge != nil || uid.IsZero() {
		http.Error(w, "user not authenticated", http.StatusUnauthorized)
		return
	}

	u := r.URL.Query().Get("url")
	if u == "" {
		http.Error(w, "Missing 'url' query parameter", http.StatusBadRequest)
		return
	}

	pu, err := url.Parse(u)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := validateURL(pu); err != nil {
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
	if cc := resp.Header.Get("Cache-Control"); cc != "" {
		w.Header().Set("Cache-Control", cc)
	}

	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		if err := json.NewEncoder(w).Encode(extractMetadata(body)); err != nil {
			http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		}
	}
}

func extractMetadata(body io.Reader) *linkPreview {
	var preview linkPreview
	var inTitleTag bool

	tokenizer := html.NewTokenizer(body)
lp:
	for {
		switch tokenizer.Next() {
		case html.ErrorToken:
			break lp

		case html.StartTagToken, html.SelfClosingTagToken:
			tag, hasAttr := tokenizer.TagName()
			tagName := atom.Lookup(tag)
			if tagName == atom.Meta && hasAttr {
				var name, property, content string
				for {
					key, val, moreAttr := tokenizer.TagAttr()
					switch atom.String(key) {
					case "name":
						name = string(val)
					case "property":
						property = string(val)
					case "content":
						content = string(val)
					}
					if !moreAttr {
						break
					}
				}

				if content != "" {
					if strings.HasPrefix(property, "og:") {
						switch property {
						case "og:title":
							preview.Title = content
						case "og:description":
							preview.Description = content
						case "og:image":
							preview.ImageURL = content
						}
					} else if name == "description" && preview.Description == "" {
						preview.Description = content
					}
				}
			} else if tagName == atom.Title {
				inTitleTag = true
			}

		case html.TextToken:
			if inTitleTag {
				if preview.Title == "" {
					preview.Title = tokenizer.Token().Data
				}
				inTitleTag = false
			}

		case html.EndTagToken:
			inTitleTag = false
		}
		if preview.Title != "" && preview.Description != "" && preview.ImageURL != "" {
			break
		}
	}

	return sanitizePreview(preview)
}

func validateURL(u *url.URL) error {
	if u.Scheme != "http" && u.Scheme != "https" {
		return &url.Error{Op: "validate", Err: errors.New("invalid scheme")}
	}

	ips, err := net.LookupIP(u.Hostname())
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

func sanitizePreview(preview linkPreview) *linkPreview {
	if utf8.RuneCountInString(preview.Title) > 80 {
		preview.Title = string([]rune(preview.Title)[:80])
	}
	if utf8.RuneCountInString(preview.Description) > 256 {
		preview.Description = string([]rune(preview.Description)[:256])
	}
	if len(preview.ImageURL) > 2000 {
		preview.ImageURL = preview.ImageURL[:2000]
	}

	return &linkPreview{
		Title:       strings.TrimSpace(preview.Title),
		Description: strings.TrimSpace(preview.Description),
		ImageURL:    strings.TrimSpace(preview.ImageURL),
	}
}
