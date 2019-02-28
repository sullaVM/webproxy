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
	"net/http/httputil"
	"os"
	"sync"
	"time"
)

var cache sync.Map

// handler checks the request method and directs the
// client to either tunnel or not.
func handler(w http.ResponseWriter, r *http.Request) {
	// Check if the URL requested is not blocked.
	if isBlocked(r.URL.Hostname()) {
		log.Printf("URL requested is blocked: %v", r.RequestURI)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	if r.Method == http.MethodConnect {
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
		log.Printf("url to block: %v", url)
		if scn.Text() == url {
			return true
		}
	}
	return false
}

// getFromCache looks for the uri in cache and returns
// it if it is available, otherwise returns nil.
func getFromCache(uri string) *[]byte {
	if c, ok := cache.Load(uri); ok {
		d := c.([]byte)
		return &d
	}
	return nil
}

// addToCache adds the most recent page in cache if
// it is not available.
func addToCache(uri string, content []byte) {
	if _, ok := cache.Load(uri); !ok {
		cache.Store(uri, content)
	}
}

// Request is an object containing information of
// a request sent to the proxy.
type Request struct {
	URL         string
	BlockedURLs []string
}

func getBlockedURLs() []string {
	// Open the tracker file.
	f, err := os.Open("tmp/block")
	if err != nil {
		log.Printf("blocked URL tracker file not opened: %v", err)
	}
	defer f.Close()

	// Check if the url is blocked by iterating through file.
	var res []string
	scn := bufio.NewScanner(f)
	for scn.Scan() {
		res = append(res, scn.Text())
	}

	if res == nil {
		res = append(res, "")
	}
	return res
}

// console serve the console management console
func console(w http.ResponseWriter, r *http.Request) {
	log.Printf("console requested")

	req := Request{
		URL:         r.RequestURI,
		BlockedURLs: getBlockedURLs(),
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
	go exchange(dst, clnt)
	go exchange(clnt, dst)

}

func exchange(dst io.WriteCloser, src io.ReadCloser) {
	defer dst.Close()
	defer src.Close()
	io.Copy(dst, src)

	// Dubp request to add to cache.

}

// handleHTTP handles the HTTP requests.
func handleHTTP(w http.ResponseWriter, r *http.Request) {
	// Display the request.
	log.Printf("HTTP Request: %v\n", r)

	start := time.Now()

	uri := r.RequestURI

	// Check if the webpage being requested is cached.
	val, ok := cache.Load(uri)
	if val == nil || !ok {
		// Data is not in cache.
		log.Printf("data not cached: %v", uri)
		// Fetch data and update cache.
		if ok := fetchAndUpdate(w, r); !ok {
			log.Printf("error handling http: cannot fetch from server")
		}
		// Check time it takes.
		time := time.Since(start)
		log.Printf("request: %v, duration: %v, cached: no", uri, time)
		return
	}
	log.Printf("cache hit for %v", uri)
	data, ok := val.([]byte)
	if data == nil || !ok {
		log.Printf("error getting data from cache: %v", uri)
		w.WriteHeader(http.StatusExpectationFailed)
		return

	}
	// Send the response to client from cache dump.
	newResp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(data)), nil)
	if newResp == nil || err != nil {
		// Response from dumped cache cannot be accessed.
		// Fetch directly from server.
		log.Printf("error reading response: %v", err)

		if ok := fetchAndUpdate(w, r); !ok {
			log.Printf("error handling http: cannot fetch from server")
		}

		// Check time it takes.
		time := time.Since(start)
		log.Printf("request: %v, duration: %v, cached: no", uri, time)
		return
	}

	// Check if the response from cache is not expired.
	exp := newResp.Header.Get("Expires")
	if exp == "" {
		log.Printf("%v: expiration not found, continue sending response", uri)
	} else {
		// Continue checking expiry.
		expDate, err := http.ParseTime(exp)
		if err != nil {
			log.Printf("%v: cannot parse expiry time, continue sending response", uri)
		} else {
			if time.Now().After(expDate) {
				// Cache is outdated. Fetch recent data.
				if ok := fetchAndUpdate(w, r); !ok {
					log.Printf("error handling http: cannot fetch from server")

					time := time.Since(start)
					log.Printf("request: %v, duration: %v, cached: no", uri, time)
					return
				}
			}
		}
	}

	time := time.Since(start)
	log.Printf("request: %v, duration: %v, cached: yes", uri, time)

	// Return the data from cache.
	copyHeader(w.Header(), newResp.Header)
	w.WriteHeader(newResp.StatusCode)
	io.Copy(w, newResp.Body)
}

func fetchAndUpdate(w http.ResponseWriter, r *http.Request) bool {
	uri := r.RequestURI

	resp, err := http.DefaultTransport.RoundTrip(r)
	if err != nil {
		log.Printf("error in handling HTTP %v: %v", uri, err)
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return false
	}
	defer resp.Body.Close()

	// If there is no error in fetching data, put it to cache.
	data, err := httputil.DumpResponse(resp, true)
	cc := resp.Header.Get("Cache-control")
	log.Printf("cache-control for %v: %v", uri, cc)
	// If response cannot be dumped of if response has "no-cache" control.
	if err != nil || cc == "no-cache" {
		log.Printf("cached failed: error dumping response for %v: %v", uri, err)
		// If response cannot be dumped, do not cache. Return the original response.
		copyHeader(w.Header(), resp.Header)
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	} else {
		// If response is dumped, add it to cache.
		addToCache(uri, data)
		// Send a response to requester from dump.
		newResp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(data)), nil)
		if err != nil {
			log.Printf("error reading response for %v: %v", uri, err)
		}
		copyHeader(w.Header(), newResp.Header)
		w.WriteHeader(newResp.StatusCode)
		io.Copy(w, newResp.Body)
	}
	return true
}

func copyHeader(dst, src http.Header) {
	for i, s := range src {
		for _, ss := range s {
			dst.Add(i, ss)
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
