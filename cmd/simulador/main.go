// Simulador de WhatsApp para probar el bot en local, sin número real ni cuenta de Meta.
//
// Hace de Meta en los dos sentidos:
//
//   - ENTRADA: lo que escribes en la consola se le manda al bot como el webhook que mandaría Meta
//     (texto, ubicación, o la opción elegida de un menú), desde un número inventado.
//   - SALIDA: levanta una Graph API falsa. El bot se arranca con WHATSAPP_API_BASE apuntando aquí,
//     así que cree que le escribe a WhatsApp pero sus mensajes caen en esta consola.
//
// Uso (el bot corriendo en :3001 con WHATSAPP_API_BASE=http://<este-host>:4000):
//
//	go run ./cmd/simulador -bot http://localhost:3001
//
// En la consola: escribir texto lo manda como mensaje; con un menú en pantalla, un número elige
// esa opción; /ubi -2.9001,-79.0059 manda una ubicación; /tel 593900000123 cambia de cliente;
// /ayuda y /salir.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// opcion es una opción de un menú que mandó el bot. El id es lo que vuelve en el webhook al
// elegirla (el bot pone ahí el texto completo; el título visible va truncado).
type opcion struct {
	ID, Titulo string
}

// menu es el último menú que el bot le mandó a un número, para poder elegir por número.
type menu struct {
	Tipo     string // "button" | "list"
	Opciones []opcion
}

type simulador struct {
	bot     string
	phoneID string

	mu       sync.Mutex
	tel      string
	nombre   string
	menus    map[string]menu
	contador int
}

func main() {
	bot := flag.String("bot", "http://localhost:3001", "URL base del bot (se le hace POST a /webhook)")
	escucha := flag.String("escucha", ":4000", "dirección de la Graph API falsa (WHATSAPP_API_BASE del bot)")
	tel := flag.String("tel", "593900000099", "número del cliente simulado (formato de WhatsApp, sin +)")
	nombre := flag.String("nombre", "Cliente Prueba", "nombre de perfil del cliente simulado")
	phoneID := flag.String("phone-id", "SIMULADOR", "phone number id que se reporta en el webhook")
	esperar := flag.Duration("esperar-al-final", 0,
		"al acabarse la entrada (mensajes por tubería), seguir mostrando respuestas del bot este tiempo")
	flag.Parse()

	s := &simulador{
		bot:     strings.TrimRight(*bot, "/"),
		phoneID: *phoneID,
		tel:     *tel,
		nombre:  *nombre,
		menus:   map[string]menu{},
	}

	go func() {
		log.Printf("Graph API falsa escuchando en %s", *escucha)
		if err := http.ListenAndServe(*escucha, http.HandlerFunc(s.metaFalsa)); err != nil {
			log.Fatalf("no se pudo levantar la Graph API falsa: %v", err)
		}
	}()

	s.imprimir("Simulador listo. Escribes como %s (%s). /ayuda para ver los comandos.", s.nombre, s.tel)
	s.consola()
	// Con los mensajes por tubería (pruebas automáticas) la entrada se acaba enseguida, pero el
	// bot contesta segundos después: sin esta espera el simulador saldría antes de verlo.
	if *esperar > 0 {
		time.Sleep(*esperar)
	}
}

// metaFalsa recibe lo que el bot cree que le manda a WhatsApp y lo muestra. También atiende
// POST /sim/enviar: el cuerpo es una línea como las de la consola, para manejar el simulador
// sin terminal interactiva (desde un script o desde otra herramienta).
func (s *simulador) metaFalsa(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost && r.URL.Path == "/sim/enviar" {
		b, _ := io.ReadAll(r.Body)
		linea := strings.TrimSpace(string(b))
		s.imprimir("👤 CLIENTE %s: %s", s.telActual(), linea)
		s.procesarLinea(linea)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/messages") {
		http.Error(w, "solo POST /{version}/{phone-id}/messages", http.StatusNotFound)
		return
	}
	b, _ := io.ReadAll(r.Body)
	var p map[string]any
	if err := json.Unmarshal(b, &p); err != nil {
		http.Error(w, "json inválido", http.StatusBadRequest)
		return
	}
	to, _ := p["to"].(string)
	s.mostrarMensajeDelBot(to, p)

	s.mu.Lock()
	s.contador++
	id := fmt.Sprintf("wamid.SIM%06d", s.contador)
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"messaging_product": "whatsapp",
		"contacts":          []map[string]string{{"input": to, "wa_id": to}},
		"messages":          []map[string]string{{"id": id}},
	})
}

func (s *simulador) mostrarMensajeDelBot(to string, p map[string]any) {
	tipo, _ := p["type"].(string)
	switch tipo {
	case "text":
		txt, _ := p["text"].(map[string]any)
		cuerpo, _ := txt["body"].(string)
		s.imprimir("🤖 BOT → %s:\n%s", to, cuerpo)
	case "interactive":
		inter, _ := p["interactive"].(map[string]any)
		m := leerMenu(inter)
		cuerpo := ""
		if body, ok := inter["body"].(map[string]any); ok {
			cuerpo, _ = body["text"].(string)
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "🤖 BOT → %s (menú %s):\n%s", to, m.Tipo, cuerpo)
		for i, o := range m.Opciones {
			fmt.Fprintf(&sb, "\n   [%d] %s", i+1, o.Titulo)
		}
		sb.WriteString("\n   (escribe el número para elegir)")
		s.mu.Lock()
		s.menus[to] = m
		s.mu.Unlock()
		s.imprimir("%s", sb.String())
	default:
		// Tipos que el bot hoy no usa (imagen, plantilla...): se muestran crudos para no perderlos.
		crudo, _ := json.MarshalIndent(p, "   ", "  ")
		s.imprimir("🤖 BOT → %s (tipo %q, sin formato):\n   %s", to, tipo, crudo)
	}
}

// leerMenu saca las opciones de un "interactive" de botones o de lista.
func leerMenu(inter map[string]any) menu {
	m := menu{}
	m.Tipo, _ = inter["type"].(string)
	accion, _ := inter["action"].(map[string]any)
	switch m.Tipo {
	case "button":
		botones, _ := accion["buttons"].([]any)
		for _, b := range botones {
			bm, _ := b.(map[string]any)
			reply, _ := bm["reply"].(map[string]any)
			id, _ := reply["id"].(string)
			titulo, _ := reply["title"].(string)
			m.Opciones = append(m.Opciones, opcion{ID: id, Titulo: titulo})
		}
	case "list":
		secciones, _ := accion["sections"].([]any)
		for _, sec := range secciones {
			sm, _ := sec.(map[string]any)
			filas, _ := sm["rows"].([]any)
			for _, f := range filas {
				fm, _ := f.(map[string]any)
				id, _ := fm["id"].(string)
				titulo, _ := fm["title"].(string)
				m.Opciones = append(m.Opciones, opcion{ID: id, Titulo: titulo})
			}
		}
	}
	return m
}

// consola lee lo que escribe el usuario y lo manda al bot como mensajes del cliente.
func (s *simulador) consola() {
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		linea := strings.TrimSpace(sc.Text())
		if linea == "/salir" {
			return
		}
		s.procesarLinea(linea)
	}
}

func (s *simulador) telActual() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tel
}

// procesarLinea interpreta una línea (comando, número de opción o texto) y la manda al bot.
func (s *simulador) procesarLinea(linea string) {
	if linea == "" {
		return
	}
	switch {
	case linea == "/ayuda":
		s.imprimir("Comandos:\n" +
			"   texto libre               → mensaje de texto\n" +
			"   1, 2, 3...                → elegir opción del último menú del bot\n" +
			"   /ubi -2.9001,-79.0059     → mandar una ubicación (pin de WhatsApp)\n" +
			"   /tel 593900000123 [Nombre] → cambiar de cliente simulado\n" +
			"   /salir")
	case strings.HasPrefix(linea, "/tel "):
		partes := strings.Fields(strings.TrimPrefix(linea, "/tel "))
		s.mu.Lock()
		s.tel = partes[0]
		if len(partes) > 1 {
			s.nombre = strings.Join(partes[1:], " ")
		}
		s.imprimir("Ahora escribes como %s (%s).", s.nombre, s.tel)
		s.mu.Unlock()
	case strings.HasPrefix(linea, "/ubi"):
		lat, lng, err := leerCoordenadas(strings.TrimSpace(strings.TrimPrefix(linea, "/ubi")))
		if err != nil {
			s.imprimir("Ubicación inválida (%v). Ejemplo: /ubi -2.9001,-79.0059", err)
			return
		}
		s.enviar(map[string]any{
			"type":     "location",
			"location": map[string]float64{"latitude": lat, "longitude": lng},
		})
	default:
		if op, tipo, ok := s.opcionElegida(linea); ok {
			clave := "button_reply"
			if tipo == "list" {
				clave = "list_reply"
			}
			s.enviar(map[string]any{
				"type": "interactive",
				"interactive": map[string]any{
					"type": clave,
					clave:  map[string]string{"id": op.ID, "title": op.Titulo},
				},
			})
			return
		}
		s.enviar(map[string]any{"type": "text", "text": map[string]string{"body": linea}})
	}
}

// opcionElegida interpreta un número como la elección de una opción del último menú del cliente
// actual. Cada menú se usa una vez: después de elegir, un número vuelve a ser texto normal.
func (s *simulador) opcionElegida(linea string) (opcion, string, bool) {
	n, err := strconv.Atoi(linea)
	if err != nil {
		return opcion{}, "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.menus[s.tel]
	if !ok || n < 1 || n > len(m.Opciones) {
		return opcion{}, "", false
	}
	delete(s.menus, s.tel)
	return m.Opciones[n-1], m.Tipo, true
}

func leerCoordenadas(txt string) (float64, float64, error) {
	partes := strings.Split(strings.ReplaceAll(txt, " ", ""), ",")
	if len(partes) != 2 {
		return 0, 0, fmt.Errorf("hacen falta latitud y longitud separadas por coma")
	}
	lat, err1 := strconv.ParseFloat(partes[0], 64)
	lng, err2 := strconv.ParseFloat(partes[1], 64)
	if err1 != nil || err2 != nil {
		return 0, 0, fmt.Errorf("no son números")
	}
	return lat, lng, nil
}

// enviar arma el webhook de Meta con el mensaje del cliente y se lo manda al bot.
func (s *simulador) enviar(mensaje map[string]any) {
	s.mu.Lock()
	tel, nombre := s.tel, s.nombre
	s.contador++
	mensaje["from"] = tel
	mensaje["id"] = fmt.Sprintf("wamid.CLI%06d", s.contador)
	mensaje["timestamp"] = strconv.FormatInt(time.Now().Unix(), 10)
	s.mu.Unlock()

	payload := map[string]any{
		"object": "whatsapp_business_account",
		"entry": []map[string]any{{
			"id": "SIMULADOR",
			"changes": []map[string]any{{
				"field": "messages",
				"value": map[string]any{
					"messaging_product": "whatsapp",
					"metadata":          map[string]string{"display_phone_number": "593000000000", "phone_number_id": s.phoneID},
					"contacts":          []map[string]any{{"wa_id": tel, "profile": map[string]string{"name": nombre}}},
					"messages":          []map[string]any{mensaje},
				},
			}},
		}},
	}
	b, _ := json.Marshal(payload)
	resp, err := http.Post(s.bot+"/webhook", "application/json", bytes.NewReader(b))
	if err != nil {
		s.imprimir("⚠️  No se pudo llegar al bot en %s: %v", s.bot, err)
		return
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		s.imprimir("⚠️  El bot respondió %d al webhook", resp.StatusCode)
	}
}

var salida sync.Mutex

// imprimir escribe en la consola sin mezclar líneas: las respuestas del bot llegan en otra
// goroutine mientras el usuario escribe.
func (s *simulador) imprimir(formato string, args ...any) {
	salida.Lock()
	defer salida.Unlock()
	fmt.Printf("\n[%s] %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(formato, args...))
}
