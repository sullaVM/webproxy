// Web Proxy App
package main

import (
	"flag"
	"html/template"
	"io"
	"log"
	"net"
	"net/http"
	"time"
)

// handler checks the request method and directs the
// client to either tunnel or not.
func handler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		// Handle tunneling.
		tunnel(w, r)
	} else {
		log.Printf(r.URL.Path)
		if r.URL.Path == "/console" {
			console(w, r)
			return
		}
		handleHTTP(w, r)
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
		URL: r.URL.Path,
	}

	tmp, err := template.ParseFiles("console.html")
	if err != nil {
		log.Printf("template parsing error: %v", err)
	}

	if r.Method == http.MethodPost {
		log.Printf("%v", r.FormValue("URL"))
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
	log.Printf("Request: %v\n", r)
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

// handleHTTP sends a response back to the client.
func handleHTTP(w http.ResponseWriter, req *http.Request) {
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		log.Printf("error in handling HTTP: %v", err)
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer resp.Body.Close()
	copyHeader(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func copyHeader(dst, src http.Header) {
	for i, s := range src {
		for _, ss := range s {
			dst.Add(i, ss)
		}
	}
}

func exchange(dst io.WriteCloser, src io.ReadCloser) {
	defer dst.Close()
	defer src.Close()
	io.Copy(dst, src)
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
