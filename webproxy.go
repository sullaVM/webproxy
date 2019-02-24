// Web Proxy App
// Sulla Montes - montess@tcd.ie - 15324631
package main

import (
	"bufio"
	"bytes"
	"flag"
	"html/template"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

var cache = make(map[string][]byte)

// handler checks the request method and directs the
// client to either tunnel or not.
func handler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		// Check if the URL requested is not blocked.
		if isBlocked(r.URL.String()) {
			log.Printf("URL requested is blocked")
			return
		}
		// Handle tunneling.
		tunnel(w, r)
	} else {
		if r.URL.Path == "/console" {
			console(w, r)
			return
		}
		handleHTTP(w, r)
	}
}

// isBlocked checks if the URL requested is blocked.
func isBlocked(url string) bool {
	// Open the tracker file.
	f, err := os.Open("tmp/block")
	if err != nil {
		log.Printf("blocked URL tracker file not opened: %v", err)
	}
	defer f.Close()

	// Check if the url is blocked by iterating through file.
	scn := bufio.NewScanner(f)
	for scn.Scan() {
		if strings.Contains(url, scn.Text()) {
			return true
		}
	}
	return false
}

// getFromCache looks for the uri in cache and returns
// it if it is available, otherwise returns nil.
func getFromCache(uri string) *[]byte {
	if c, ok := cache[uri]; ok {
		return &c
	}
	return nil
}

// addToCache adds the most recent page in cache if
// it is not available.
func addToCache(uri string, content []byte) {
	if _, ok := cache[uri]; !ok {
		cache[uri] = content
	}
}

// Request is an object containing information of
// a request sent to the proxy.
type Request struct {
	URL string
}

// console serve the console management console
func console(w http.ResponseWriter, r *http.Request) {
	log.Printf("console requested")

	req := Request{
		URL: r.RequestURI,
	}

	tmp, err := template.ParseFiles("console.html")
	if err != nil {
		log.Printf("template parsing error: %v", err)
	}

	if r.Method == http.MethodPost {
		url := r.FormValue("URL") + "\n"
		log.Printf("%v", url)

		// Append URL into a tracker file.
		f, err := os.OpenFile("tmp/block", os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			log.Printf("error opening file: %v", err)
			f, err = os.Create("tmp/block")
		}
		defer f.Close()

		if url == "\n" {
			log.Printf("error adding URL: empty line")
		} else {
			if _, err = f.WriteString(url); err != nil {
				log.Printf("error writing to file: %v", err)
			}
		}

		tmp.Execute(w, nil)
		return
	}

	err = tmp.Execute(w, req)
	if err != nil {
		log.Printf("template executing error: %v", err)
	}

}

// tunnel allows a client and destination server to
// communicate through this proxy, using go routines.
func tunnel(w http.ResponseWriter, r *http.Request) {
	// Display the request.
	log.Printf("HTTPS Request: %v\n", r)

	// Check if the webpage exists in cache.
	if data := getFromCache(r.RequestURI); data != nil {
		log.Printf("%v is taken form cache", r.RequestURI)
		log.Printf("data: %v", data)
		w.Write(*data)
		return
	}

	// Connect to desination.
	dst, err := net.DialTimeout("tcp", r.Host, 10*time.Second)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
	// Hijack the connection between client to destination.
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "server does not support hijacking", http.StatusInternalServerError)
		return
	}
	clnt, _, err := hj.Hijack()
	if err != nil {
		log.Printf("error hijacking: %v", err)
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}

	// Allow client and destination to exchange packets through proxy.
	go exchange(r.RequestURI, dst, clnt)
	go exchange(r.RequestURI, clnt, dst)

}

func exchange(uri string, dst io.WriteCloser, src io.ReadCloser) {
	defer dst.Close()
	defer src.Close()
	io.Copy(dst, src)

	// Dubp request to add to cache.

}

// handleHTTP handles the HTTP requests.
func handleHTTP(w http.ResponseWriter, r *http.Request) {
	// Display the request.
	log.Printf("HTTP Request: %v\n", r)

	// Check cache.
	data := cache[r.RequestURI]
	if data != nil {
		// Cache hit.
		w.Write(data)
		return
	}

	// Obtain direct from source.
	resp, err := http.DefaultTransport.RoundTrip(r)
	if err != nil {
		log.Printf("error in handling HTTP: %v", err)
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer resp.Body.Close()

	buf := new(bytes.Buffer)
	copyHeader(buf, w.Header(), resp.Header)
	io.Copy(buf, resp.Body)
	data = buf.Bytes()
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, buf)

	log.Printf("cache: %v", string(data))
}

func copyHeader(buf *bytes.Buffer, dst, src http.Header) {
	for i, s := range src {
		for _, ss := range s {
			dst.Add(i, ss)
			buf.Write([]byte(ss))
		}
	}
}

func main() {
	var protocol string

	// Declare the type of HTTP protocol that will be used.
	flag.StringVar(&protocol, "proto", "https", "Proxy protocol")
	flag.Parse()

	if protocol != "http" && protocol != "https" {
		log.Fatal("protocol is invalid; must be HTTP or HTTPs")
	}

	h := http.HandlerFunc(handler)

	// Set up a server.
	s := &http.Server{
		Addr:         ":8080",
		Handler:      h,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	log.Fatal(s.ListenAndServe())
}
