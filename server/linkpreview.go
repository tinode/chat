package main

import (
	"encoding/json"
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
	Transport: &http.Transport{},
	Timeout:   time.Second * 2,
}

// previewLink handles the HTTP request, fetches the URL, and returns the link preview.
func previewLink(w http.ResponseWriter, r *http.Request) {
	u := r.URL.Query().Get("url")
	if u == "" {
		http.Error(w, "Missing 'url' query parameter", http.StatusBadRequest)
		return
	}

	parsedURL, err := url.Parse(u)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		http.Error(w, "invalid schema", http.StatusBadRequest)
		return
	}

	ips, err := net.LookupIP(parsedURL.Hostname())
	if err != nil {
		http.Error(w, "invalid host", http.StatusBadRequest)
		return
	}
	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsPrivate() {
			http.Error(w, "non routable IP address", http.StatusBadRequest)
			return
		}
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

	body := io.LimitReader(resp.Body, 2*1024) // 2KB limit
	doc, err := html.Parse(body)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("{}"))
		return
	}

	linkPreview := extractMetadata(doc)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(linkPreview); err != nil {
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
	}
}

func extractMetadata(n *html.Node) linkPreview {
	var preview linkPreview

	var traverse func(*html.Node)
	traverse = func(n *html.Node) {
		if n.Type == html.ElementNode && strings.ToLower(n.Data) == "meta" {
			var name, property, content string
			for _, attr := range n.Attr {
				switch attr.Key {
				case "name":
					name = attr.Val
				case "property":
					property = attr.Val
				case "content":
					content = attr.Val
				}
			}

			if strings.HasPrefix(property, "og:") && content != "" {
				if property == "og:title" {
					preview.Title = content
				} else if property == "og:description" {
					preview.Description = content
				} else if property == "og:image" {
					preview.ImageURL = content
				}
			} else if name == "description" && preview.Description == "" {
				preview.Description = content
			}
		}

		for child := n.FirstChild; child != nil; child = child.NextSibling {
			traverse(child)
		}
	}

	traverse(n)

	if preview.Title == "" {
		preview.Title = extractTitle(n)
	}

	return preview
}

func extractTitle(n *html.Node) string {
	if n.Type == html.ElementNode && n.Data == "title" {
		if n.FirstChild != nil {
			return n.FirstChild.Data
		}
	}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		title := extractTitle(child)
		if title != "" {
			return title
		}
	}
	return ""
}
