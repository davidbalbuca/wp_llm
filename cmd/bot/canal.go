package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"

	"wp-llm-gas/internal/agent"
	"wp-llm-gas/internal/config"
	"wp-llm-gas/internal/conversation"
	"wp-llm-gas/internal/georoutes"
)

// canalBot es un bot completo: su config (con su canal y, en bodegas, su sesión), su almacén, su
// agente y su cliente del backend. El bot de Ubi tiene uno; el de bodegas, uno por bodega. Que todo
// viaje junto es lo que impide cruces: lo que hace un canalBot sale por SU canal y queda en SU store.
type canalBot struct {
	cfg   config.Config
	store conversation.Store
	ag    *agent.Agent
	gr    *georoutes.Client
}

// elegirBot decide qué bot atiende un aviso del backend (/internal/*). Si no hay, ya respondió.
type elegirBot func(w http.ResponseWriter, r *http.Request) (*canalBot, bool)

// sesionDelAviso lee la `sesion` del aviso (query o cuerpo JSON) sin consumir el cuerpo. El backend
// la manda SOLO en los avisos de pedidos de bodega (bot_destino).
func sesionDelAviso(r *http.Request) string {
	if s := r.URL.Query().Get("sesion"); s != "" {
		return s
	}
	if r.Body == nil || r.Method == http.MethodGet {
		return ""
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	r.Body = io.NopCloser(bytes.NewReader(body))
	var p struct {
		Sesion string `json:"sesion"`
	}
	_ = json.Unmarshal(body, &p)
	return p.Sesion
}

// botFijo es el elegir del bot de Ubi. Un aviso con `sesion` es de un pedido de bodega que llegó aquí
// por error de configuración: se rechaza, para que el cliente de la bodega no reciba un mensaje desde
// el número de Ubi.
func botFijo(b *canalBot) elegirBot {
	return func(w http.ResponseWriter, r *http.Request) (*canalBot, bool) {
		if s := sesionDelAviso(r); s != "" {
			log.Printf("[canal] un aviso de la sesión de bodega %s llegó al bot de Ubi; se rechaza", s)
			w.WriteHeader(http.StatusConflict)
			return nil, false
		}
		return b, true
	}
}
