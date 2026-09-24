package agent

import (
	"context"
	"strings"
	"testing"

	"wp-llm-gas/internal/conversation"
)

// COMPARTIR LA UBICACIÓN ES UNA PREGUNTA: "¿llegan aquí?". Hay que responderla.
//
// INCIDENTE 24/09 (593939235151): el cliente escribió "Deseo pedir GAS / En qué parte de cuenca
// da su servicio". El bot le pidió la ubicación "para confirmarte si llegamos justo a tu
// dirección", el cliente la mandó... y el bot contestó con el menú de colores. Nunca le dijo que
// sí. Siguió todo el pedido —color, cantidad, cédula, nombre— sin saber si le iba a llegar.
//
// El backend YA devolvía el sector en checkCoverage; el bot leía solo `cubierto` y tiraba el
// nombre. Aquí se comprueba que ahora se le diga.

// EL CASO REAL: el turno tras la ubicación acaba en MENÚ. Es el más importante de este archivo,
// porque el texto de la respuesta NO se envía cuando hay menú: si la confirmación solo se
// antepusiera al texto, el cliente seguiría sin enterarse. Debe viajar en el cuerpo del menú.
func TestIncidente_LaCoberturaSeConfirmaAunqueElTurnoAcabeEnMenu(t *testing.T) {
	const from = "593939235151"
	store := conversation.NewMemStore()
	store.SetLocation(from, -2.913286, -78.953448)
	store.SetSectorCubierto(from, "TOTORACOCHA")

	var cuerpoEnviado string
	ag := agentIncidente(&modeloQueDice{respuestas: []string{"¿Cuál prefieres?"}}, store)
	ag.catalog = catalogoConEquivalencias()
	ag.enviarMenu = func(_, cuerpo string, _ []string) error {
		cuerpoEnviado = cuerpo
		return nil
	}

	if err := ag.mandarMenu(from, "¿Cuál prefieres?", []string{"BLANCO", "AMARILLO"}); err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if !strings.Contains(cuerpoEnviado, "TOTORACOCHA") {
		t.Fatalf("el menú salió sin confirmar la cobertura: %q", cuerpoEnviado)
	}
	if !strings.Contains(strings.ToLower(cuerpoEnviado), "llegamos") {
		t.Errorf("no se le dijo que SÍ llegamos: %q", cuerpoEnviado)
	}
	// Y la pregunta original sigue ahí: la confirmación se añade, no reemplaza.
	if !strings.Contains(cuerpoEnviado, "¿Cuál prefieres?") {
		t.Errorf("se perdió la pregunta del menú: %q", cuerpoEnviado)
	}
}

// El turno normal (respuesta en texto) también confirma.
func TestLaCoberturaSeConfirmaEnElTextoDeLaRespuesta(t *testing.T) {
	const from = "593939235152"
	store := conversation.NewMemStore()
	store.SetLocation(from, -2.91, -78.95)
	store.SetSectorCubierto(from, "SININCAY")
	ag := agentIncidente(&modeloQueDice{respuestas: []string{
		"Perfecto 👍 ¿Cuántos cilindros necesitas?",
	}}, store)

	res, err := ag.HandleMessage(context.Background(), from, "He compartido mi ubicación actual.")
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if !strings.Contains(res.Texto, "SININCAY") {
		t.Errorf("no se le confirmó su sector: %q", res.Texto)
	}
}

// SE DICE UNA SOLA VEZ. Repetir "sí llegamos a tu zona" en cada mensaje suena a disco rayado y
// tapa lo que el cliente está preguntando en ese momento.
func TestLaConfirmacionNoSeRepiteEnCadaTurno(t *testing.T) {
	const from = "593939235153"
	store := conversation.NewMemStore()
	store.SetLocation(from, -2.91, -78.95)
	store.SetSectorCubierto(from, "MONAY")
	ag := agentIncidente(&modeloQueDice{respuestas: []string{
		"¿Cuántos cilindros necesitas?",
		"¡Listo! ¿Confirmamos el pedido?",
	}}, store)

	primera, err := ag.HandleMessage(context.Background(), from, "He compartido mi ubicación actual.")
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if !strings.Contains(primera.Texto, "MONAY") {
		t.Fatalf("la primera respuesta no confirmó la zona: %q", primera.Texto)
	}
	segunda, err := ag.HandleMessage(context.Background(), from, "2")
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if strings.Contains(segunda.Texto, "MONAY") {
		t.Errorf("se repitió la confirmación en el turno siguiente: %q", segunda.Texto)
	}
}

// Si el modelo YA lo dijo (el prompt se lo pide), el candado no duplica. Un cliente que lee
// "Sí llegamos a tu zona (MONAY)" dos veces en el mismo mensaje ve un bot roto.
func TestSiElModeloYaConfirmoNoSeDuplica(t *testing.T) {
	const from = "593939235154"
	store := conversation.NewMemStore()
	store.SetLocation(from, -2.91, -78.95)
	store.SetSectorCubierto(from, "MONAY")
	ag := agentIncidente(&modeloQueDice{respuestas: []string{
		"¡Buenas noticias! Sí llegamos a tu zona (MONAY) 🎉 ¿Cuántos cilindros necesitas?",
	}}, store)

	res, err := ag.HandleMessage(context.Background(), from, "He compartido mi ubicación actual.")
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if strings.Count(res.Texto, "MONAY") != 1 {
		t.Errorf("la confirmación salió duplicada: %q", res.Texto)
	}
}

// Sin sector verificado no se confirma NADA. Es la mitad peligrosa: afirmar cobertura sin que la
// geocerca lo haya dicho es justo lo que el candado hermano (revisarCoberturaAfirmada) impide.
func TestSinSectorVerificadoNoSeConfirmaNada(t *testing.T) {
	const from = "593939235155"
	store := conversation.NewMemStore()
	ag := agentIncidente(&modeloQueDice{respuestas: []string{"¿Cuántos cilindros necesitas?"}}, store)

	res, err := ag.HandleMessage(context.Background(), from, "hola")
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if strings.Contains(strings.ToLower(res.Texto), "llegamos a tu zona") {
		t.Errorf("se confirmó cobertura sin haberla verificado: %q", res.Texto)
	}
}

func TestConfirmaElSectorReconoceLasDosFormas(t *testing.T) {
	// Por el nombre del sector (lo que pide el prompt).
	if !confirmaElSector("¡Sí llegamos! Estás en TOTORACOCHA 🎉", "TOTORACOCHA") {
		t.Error("no reconoció la confirmación por nombre de sector")
	}
	// Y por la frase, aunque el modelo no nombre el sector.
	if !confirmaElSector("¡Buenas noticias! Sí llegamos a tu zona 🎉", "TOTORACOCHA") {
		t.Error("no reconoció la confirmación por la frase")
	}
	// Lo que NO es una confirmación: la pregunta del menú a secas.
	if confirmaElSector("¿Cuál prefieres?", "TOTORACOCHA") {
		t.Error("falso positivo con un menú cualquiera")
	}
	// Ni una negativa (afirmaSecuencia descarta negaciones).
	if confirmaElSector("por ahora no llegamos a tu zona", "SININCAY") {
		t.Error("falso positivo con una negativa")
	}
}
