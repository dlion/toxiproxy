package main

import (
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"

	"github.com/Sirupsen/logrus"
	"github.com/gorilla/mux"
)

var (
	ErrInvalidStream = errors.New("invalid stream")
	ErrToxicNotFound = errors.New("toxic not found")
)

type server struct {
	collection *ProxyCollection
}

func NewServer(collection *ProxyCollection) *server {
	return &server{
		collection: collection,
	}
}

func (server *server) Listen(host string, port string) {
	r := mux.NewRouter()
	r.HandleFunc("/reset", server.ResetState).Methods("GET")
	r.HandleFunc("/proxies", server.ProxyIndex).Methods("GET")
	r.HandleFunc("/proxies", server.ProxyCreate).Methods("POST")
	r.HandleFunc("/proxies/{proxy}", server.ProxyShow).Methods("GET")
	r.HandleFunc("/proxies/{proxy}", server.ProxyUpdate).Methods("POST")
	r.HandleFunc("/proxies/{proxy}", server.ProxyDelete).Methods("DELETE")
	r.HandleFunc("/proxies/{proxy}/{stream}/toxics", server.ToxicIndex).Methods("GET")
	r.HandleFunc("/proxies/{proxy}/{stream}/toxics", server.ToxicCreate).Methods("POST")
	r.HandleFunc("/proxies/{proxy}/{stream}/toxics/{toxic}", server.ToxicShow).Methods("GET")
	r.HandleFunc("/proxies/{proxy}/{stream}/toxics/{toxic}", server.ToxicUpdate).Methods("POST")
	r.HandleFunc("/proxies/{proxy}/{stream}/toxics/{toxic}", server.ToxicDelete).Methods("DELETE")

	r.HandleFunc("/version", server.Version).Methods("GET")
	http.Handle("/", r)

	logrus.WithFields(logrus.Fields{
		"host":    host,
		"port":    port,
		"version": Version,
	}).Info("API HTTP server starting")

	err := http.ListenAndServe(net.JoinHostPort(host, port), nil)
	if err != nil {
		log.Fatal("ListenAndServe: ", err)
	}
}

func (server *server) ProxyIndex(response http.ResponseWriter, request *http.Request) {
	proxies := server.collection.Proxies()
	marshalData := make(map[string]interface{}, len(proxies))

	for name, proxy := range proxies {
		marshalData[name] = proxyWithToxics(proxy)
	}

	data, err := json.Marshal(marshalData)
	if err != nil {
		response.Header().Set("Content-Type", "application/json")
		http.Error(response, server.apiError(err, http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	response.Header().Set("Content-Type", "application/json")
	_, err = response.Write(data)
	if err != nil {
		logrus.Warn("ProxyIndex: Failed to write response to client", err)
	}
}

func (server *server) ResetState(response http.ResponseWriter, request *http.Request) {
	proxies := server.collection.Proxies()

	for _, proxy := range proxies {
		err := proxy.Start()
		if err != nil && err != ErrProxyAlreadyStarted {
			response.Header().Set("Content-Type", "application/json")
			http.Error(response, server.apiError(err, http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		proxy.upToxics.ResetToxics()
		proxy.downToxics.ResetToxics()
	}

	response.WriteHeader(http.StatusNoContent)
	_, err := response.Write(nil)
	if err != nil {
		logrus.Warn("ResetState: Failed to write headers to client", err)
	}
}

func (server *server) ProxyCreate(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Content-Type", "application/json")

	// Default fields enable to proxy right away
	input := Proxy{Enabled: true}
	err := json.NewDecoder(request.Body).Decode(&input)
	if err != nil {
		http.Error(response, server.apiError(err, http.StatusBadRequest), http.StatusBadRequest)
		return
	}

	if len(input.Name) < 1 {
		http.Error(response, server.apiError(errors.New("Missing required field: name"), http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	if len(input.Upstream) < 1 {
		http.Error(response, server.apiError(errors.New("Missing required field: upstream"), http.StatusBadRequest), http.StatusBadRequest)
		return
	}

	proxy := NewProxy()
	proxy.Name = input.Name
	proxy.Listen = input.Listen
	proxy.Upstream = input.Upstream
	if input.Enabled {
		err = proxy.Start()
		if err != nil {
			http.Error(response, server.apiError(err, http.StatusConflict), http.StatusConflict)
			return
		}
	}

	err = server.collection.Add(proxy)
	if err != nil {
		http.Error(response, server.apiError(err, http.StatusConflict), http.StatusConflict)
		return
	}

	data, err := json.Marshal(proxyWithToxics(proxy))
	if err != nil {
		http.Error(response, server.apiError(err, http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	response.WriteHeader(http.StatusCreated)
	_, err = response.Write(data)
	if err != nil {
		logrus.Warn("ProxyCreate: Failed to write response to client", err)
	}
}

func (server *server) ProxyUpdate(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Content-Type", "application/json")
	vars := mux.Vars(request)

	proxy, err := server.collection.Get(vars["proxy"])
	if err != nil {
		http.Error(response, server.apiError(err, http.StatusNotFound), http.StatusNotFound)
		return
	}

	// Default fields are the same as existing proxy
	input := Proxy{Listen: proxy.Listen, Upstream: proxy.Upstream, Enabled: proxy.Enabled}
	err = json.NewDecoder(request.Body).Decode(&input)
	if err != nil {
		http.Error(response, server.apiError(err, http.StatusBadRequest), http.StatusBadRequest)
		return
	}

	err = proxy.Update(&input)
	if err != nil {
		http.Error(response, server.apiError(err, http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	data, err := json.Marshal(proxyWithToxics(proxy))
	if err != nil {
		http.Error(response, server.apiError(err, http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	_, err = response.Write(data)
	if err != nil {
		logrus.Warn("ProxyUpdate: Failed to write response to client", err)
	}
}

func (server *server) ProxyDelete(response http.ResponseWriter, request *http.Request) {
	vars := mux.Vars(request)

	err := server.collection.Remove(vars["proxy"])
	if err != nil {
		response.Header().Set("Content-Type", "application/json")
		http.Error(response, server.apiError(err, http.StatusNotFound), http.StatusNotFound)
		return
	}

	response.WriteHeader(http.StatusNoContent)
	_, err = response.Write(nil)
	if err != nil {
		logrus.Warn("ProxyDelete: Failed to write headers to client", err)
	}
}

func (server *server) ProxyShow(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Content-Type", "application/json")
	vars := mux.Vars(request)

	proxy, err := server.collection.Get(vars["proxy"])
	if err != nil {
		http.Error(response, server.apiError(err, http.StatusNotFound), http.StatusNotFound)
		return
	}

	data, err := json.Marshal(proxyWithToxics(proxy))
	if err != nil {
		http.Error(response, server.apiError(err, http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	_, err = response.Write(data)
	if err != nil {
		logrus.Warn("ProxyShow: Failed to write response to client", err)
	}
}

func (server *server) ToxicIndex(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Content-Type", "application/json")
	vars := mux.Vars(request)

	proxy, err := server.collection.Get(vars["proxy"])
	if err != nil {
		http.Error(response, server.apiError(err, http.StatusNotFound), http.StatusNotFound)
		return
	}

	var data []byte
	switch vars["stream"] {
	case "upstream":
		data, err = json.Marshal(proxy.upToxics.GetToxicMap())
	case "downstream":
		data, err = json.Marshal(proxy.downToxics.GetToxicMap())
	default:
		http.Error(response, server.apiError(ErrInvalidStream, http.StatusBadRequest), http.StatusBadRequest)
	}
	if err != nil {
		http.Error(response, server.apiError(err, http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	_, err = response.Write(data)
	if err != nil {
		logrus.Warn("ToxicIndex: Failed to write response to client", err)
	}
}

func (server *server) ToxicShow(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Content-Type", "application/json")
	vars := mux.Vars(request)

	proxy, err := server.collection.Get(vars["proxy"])
	if err != nil {
		http.Error(response, server.apiError(err, http.StatusNotFound), http.StatusNotFound)
		return
	}

	var toxic Toxic
	switch vars["stream"] {
	case "upstream":
		toxic = proxy.upToxics.GetToxic(vars["toxic"])
	case "downstream":
		toxic = proxy.downToxics.GetToxic(vars["toxic"])
	default:
		http.Error(response, server.apiError(ErrInvalidStream, http.StatusBadRequest), http.StatusBadRequest)
	}
	if toxic == nil {
		http.Error(response, server.apiError(ErrToxicNotFound, http.StatusNotFound), http.StatusNotFound)
		return
	}

	data, err := json.Marshal(toxic)
	if err != nil {
		http.Error(response, server.apiError(err, http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	_, err = response.Write(data)
	if err != nil {
		logrus.Warn("ToxicShow: Failed to write response to client", err)
	}
}

func (server *server) ToxicCreate(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Content-Type", "application/json")
	vars := mux.Vars(request)

	proxy, err := server.collection.Get(vars["proxy"])
	if err != nil {
		http.Error(response, server.apiError(err, http.StatusNotFound), http.StatusNotFound)
		return
	}

	var toxic Toxic
	switch vars["stream"] {
	case "upstream":
		toxic, err = proxy.upToxics.SetToxicJson(vars["toxic"], request.Body)
	case "downstream":
		toxic, err = proxy.downToxics.SetToxicJson(vars["toxic"], request.Body)
	default:
		http.Error(response, server.apiError(ErrInvalidStream, http.StatusBadRequest), http.StatusBadRequest)
	}
	if err != nil {
		http.Error(response, server.apiError(err, http.StatusBadRequest), http.StatusBadRequest)
		return
	}

	data, err := json.Marshal(toxic)
	if err != nil {
		http.Error(response, server.apiError(err, http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	_, err = response.Write(data)
	if err != nil {
		logrus.Warn("ToxicCreate: Failed to write response to client", err)
	}
}

func (server *server) ToxicUpdate(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Content-Type", "application/json")
	vars := mux.Vars(request)

	proxy, err := server.collection.Get(vars["proxy"])
	if err != nil {
		http.Error(response, server.apiError(err, http.StatusNotFound), http.StatusNotFound)
		return
	}

	var toxic Toxic
	switch vars["stream"] {
	case "upstream":
		toxic, err = proxy.upToxics.SetToxicJson(vars["toxic"], request.Body)
	case "downstream":
		toxic, err = proxy.downToxics.SetToxicJson(vars["toxic"], request.Body)
	default:
		http.Error(response, server.apiError(ErrInvalidStream, http.StatusBadRequest), http.StatusBadRequest)
	}
	if err != nil {
		http.Error(response, server.apiError(err, http.StatusBadRequest), http.StatusBadRequest)
		return
	}

	data, err := json.Marshal(toxic)
	if err != nil {
		http.Error(response, server.apiError(err, http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	_, err = response.Write(data)
	if err != nil {
		logrus.Warn("ToxicUpdate: Failed to write response to client", err)
	}
}

func (server *server) ToxicDelete(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Content-Type", "application/json")
	vars := mux.Vars(request)

	proxy, err := server.collection.Get(vars["proxy"])
	if err != nil {
		http.Error(response, server.apiError(err, http.StatusNotFound), http.StatusNotFound)
		return
	}

	var toxic Toxic
	switch vars["stream"] {
	case "upstream":
		toxic, err = proxy.upToxics.SetToxicJson(vars["toxic"], request.Body)
	case "downstream":
		toxic, err = proxy.downToxics.SetToxicJson(vars["toxic"], request.Body)
	default:
		http.Error(response, server.apiError(ErrInvalidStream, http.StatusBadRequest), http.StatusBadRequest)
	}
	if err != nil {
		http.Error(response, server.apiError(err, http.StatusBadRequest), http.StatusBadRequest)
		return
	}

	data, err := json.Marshal(toxic)
	if err != nil {
		http.Error(response, server.apiError(err, http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	_, err = response.Write(data)
	if err != nil {
		logrus.Warn("ToxicDelete: Failed to write response to client", err)
	}
}

func (server *server) Version(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Content-Type", "text/plain")
	_, err := response.Write([]byte(Version))
	if err != nil {
		logrus.Warn("Version: Failed to write response to client", err)
	}
}

func (server *server) apiError(err error, code int) string {
	data, err2 := json.Marshal(struct {
		Title  string `json:"title"`
		Status int    `json:"status"`
	}{err.Error(), code})
	if err2 != nil {
		logrus.Warn("Error json encoding error (╯°□°）╯︵ ┻━┻", err2)
		return ""
	}
	return string(data)
}

func proxyWithToxics(proxy *Proxy) (result struct {
	*Proxy
	UpstreamToxics   map[string]Toxic `json:"upstream_toxics"`
	DownstreamToxics map[string]Toxic `json:"downstream_toxics"`
}) {
	result.Proxy = proxy
	result.UpstreamToxics = proxy.upToxics.GetToxicMap()
	result.DownstreamToxics = proxy.downToxics.GetToxicMap()
	return
}
