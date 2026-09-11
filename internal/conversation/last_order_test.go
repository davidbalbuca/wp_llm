package conversation

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// El último pedido guarda TAMBIÉN a dónde se entregó. Se prueba contra los DOS backends: si
// sqlite y memoria divergen, el bot se comporta distinto en dev y en producción.
func TestLastOrderGuardaElDestino(t *testing.T) {
	backends := map[string]func() Store{
		"mem": func() Store { return NewMemStore() },
		"sqlite": func() Store {
			s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "bot.db"), 15)
			if err != nil {
				t.Fatalf("abrir sqlite: %v", err)
			}
			return s
		},
	}
	for nombre, abrir := range backends {
		t.Run(nombre, func(t *testing.T) {
			store := abrir()
			const from = "593999000080"
			store.SetLastOrder(from, LastOrder{
				Producto: "GAS 15KG", Color: "BLANCO", Cantidad: 2, Fecha: "09/09/2026",
				Latitude: -2.898, Longitude: -79.002, Alias: "Casa", Direccion: "Av. Solano 123",
			})

			last, hay := store.GetLastOrder(from)
			if !hay {
				t.Fatal("no se recuperó el último pedido")
			}
			if last.Alias != "Casa" || last.Direccion != "Av. Solano 123" {
				t.Errorf("se perdió el destino: alias=%q dir=%q", last.Alias, last.Direccion)
			}
			if last.Latitude != -2.898 || last.Longitude != -79.002 {
				t.Errorf("se perdieron las coordenadas: %f, %f", last.Latitude, last.Longitude)
			}
			if last.Destino() != "Casa" {
				t.Errorf("el destino mostrado debía ser el nombre del cliente: %q", last.Destino())
			}
		})
	}
}

// NO-REGRESIÓN CRÍTICA: en producción la tabla last_orders YA EXISTE, así que el
// CREATE TABLE IF NOT EXISTS del esquema no la actualiza. Sin la migración de columnas, al
// arrancar contra la base de prod TODA lectura de last_orders fallaría ("no such column") y
// ningún cliente podría repetir su pedido.
//
// Este test simula ese arranque: crea la tabla con el esquema VIEJO, mete una fila como las que
// ya hay en producción, y abre el store como lo haría el bot al desplegarse.
func TestBaseDeProduccionSinLasColumnasNuevasSigueFuncionando(t *testing.T) {
	ruta := filepath.Join(t.TempDir(), "prod.db")

	// 1) Base "vieja": la tabla tal como está hoy en el servidor, con un pedido dentro.
	db, err := sql.Open("sqlite", ruta)
	if err != nil {
		t.Fatalf("abrir base: %v", err)
	}
	if _, err := db.Exec(`
        CREATE TABLE last_orders (
            phone      TEXT    PRIMARY KEY,
            producto   TEXT,
            color      TEXT,
            cantidad   INTEGER,
            fecha      TEXT,
            updated_at INTEGER NOT NULL
        );
        INSERT INTO last_orders VALUES('593999000081','GAS 15KG','BLANCO',2,'01/09/2026',0);`); err != nil {
		t.Fatalf("preparar la base vieja: %v", err)
	}
	db.Close()

	// 2) Arranque del bot nuevo sobre esa base: no puede fallar.
	store, err := NewSQLiteStore(ruta, 15)
	if err != nil {
		t.Fatalf("el bot NO ARRANCA sobre la base de producción: %v", err)
	}

	// 3) El pedido que ya estaba se sigue leyendo, y sin destino (nadie lo guardó entonces).
	last, hay := store.GetLastOrder("593999000081")
	if !hay {
		t.Fatal("se perdió un pedido que ya existía en producción: el cliente no podría repetirlo")
	}
	if last.Color != "BLANCO" || last.Cantidad != 2 {
		t.Errorf("el pedido viejo se leyó mal: %+v", last)
	}
	if last.Destino() != "" {
		t.Errorf("un pedido viejo no tiene destino guardado y no puede inventarse uno: %q", last.Destino())
	}

	// 4) Y el pedido siguiente ya guarda destino con normalidad.
	store.SetLastOrder("593999000081", LastOrder{
		Producto: "GAS 15KG", Color: "BLANCO", Cantidad: 2, Fecha: "09/09/2026",
		Latitude: -2.898, Longitude: -79.002, Alias: "Casa",
	})
	if last, _ := store.GetLastOrder("593999000081"); last.Destino() != "Casa" {
		t.Errorf("tras migrar, el destino no se guardó: %q", last.Destino())
	}
}

// La migración corre en CADA arranque: tiene que ser idempotente o el bot dejaría de arrancar
// en el segundo deploy.
func TestElArranqueRepetidoNoRompeLaBase(t *testing.T) {
	ruta := filepath.Join(t.TempDir(), "bot.db")
	for intento := 1; intento <= 3; intento++ {
		if _, err := NewSQLiteStore(ruta, 15); err != nil {
			t.Fatalf("arranque %d falló: %v (el bot no volvería a levantar tras un reinicio)", intento, err)
		}
	}
}

// Mismo riesgo con la columna perfil_whatsapp (10/09): la tabla profiles YA existe en el
// servidor, así que la columna nueva solo entra por el ALTER idempotente. Si no entrara, toda
// lectura de perfiles reventaría al arrancar y el bot no atendería a nadie.
func TestBaseDeProduccionSinPerfilWhatsAppSigueFuncionando(t *testing.T) {
	ruta := filepath.Join(t.TempDir(), "prod.db")

	// 1) Base "vieja": profiles como está hoy en el servidor, con un cliente registrado.
	db, err := sql.Open("sqlite", ruta)
	if err != nil {
		t.Fatalf("abrir base: %v", err)
	}
	if _, err := db.Exec(`
        CREATE TABLE profiles (
            phone          TEXT PRIMARY KEY,
            identificacion TEXT,
            nombres        TEXT,
            correo         TEXT,
            updated_at     INTEGER NOT NULL
        );
        INSERT INTO profiles VALUES('593963646872','0152648176','Guillermo Pacheco','',0);`); err != nil {
		t.Fatalf("preparar la base vieja: %v", err)
	}
	db.Close()

	// 2) Arranque del bot nuevo sobre esa base: no puede fallar.
	store, err := NewSQLiteStore(ruta, 15)
	if err != nil {
		t.Fatalf("el bot NO ARRANCA sobre la base de producción: %v", err)
	}

	// 3) El cliente que ya estaba se sigue leyendo, sin nombre de WhatsApp (nadie lo guardó).
	p, hay := store.GetProfile("593963646872")
	if !hay || p.Identificacion != "0152648176" {
		t.Fatalf("se perdió un cliente que ya existía en producción: ok=%v perfil=%+v", hay, p)
	}
	if p.PerfilWhatsApp != "" {
		t.Errorf("un perfil viejo no tiene nombre de WhatsApp y no puede inventarse uno: %q", p.PerfilWhatsApp)
	}

	// 4) Y desde el primer mensaje ya se guarda con normalidad, sin tocar sus datos.
	store.SetPerfilWhatsApp("593963646872", "Guillermo Pacheco")
	p, _ = store.GetProfile("593963646872")
	if p.PerfilWhatsApp != "Guillermo Pacheco" {
		t.Errorf("tras migrar, el nombre de WhatsApp no se guardó: %+v", p)
	}
	if p.Identificacion != "0152648176" {
		t.Errorf("la migración le borró la cédula: %+v", p)
	}
}

// Las LÍNEAS multicolor (Fase C1) sobreviven al viaje por los dos backends, y una base de
// producción sin la columna items sigue leyéndose (fila vieja = pedido de un color).
func TestLastOrderMulticolorEnLosDosBackends(t *testing.T) {
	backends := map[string]func() Store{
		"mem": func() Store { return NewMemStore() },
		"sqlite": func() Store {
			s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "bot.db"), 15)
			if err != nil {
				t.Fatalf("abrir sqlite: %v", err)
			}
			return s
		},
	}
	for nombre, abrir := range backends {
		t.Run(nombre, func(t *testing.T) {
			store := abrir()
			const from = "593999000082"
			store.SetLastOrder(from, LastOrder{
				Producto: "GAS 15KG", Color: "BLANCO", Cantidad: 1, Fecha: "10/09/2026",
				Items: []ItemPedido{{Color: "BLANCO", Cantidad: 1}, {Color: "AMARILLO", Cantidad: 1}},
			})
			last, hay := store.GetLastOrder(from)
			if !hay || len(last.Items) != 2 {
				t.Fatalf("las líneas multicolor se perdieron: %+v (hay=%v)", last, hay)
			}
			if li := last.ItemsDelPedido(); len(li) != 2 || li[1].Color != "AMARILLO" {
				t.Errorf("ItemsDelPedido no devuelve la lista completa: %+v", li)
			}

			// Un pedido de UN color después NO arrastra la lista del anterior.
			store.SetLastOrder(from, LastOrder{Producto: "GAS 15KG", Color: "AZUL", Cantidad: 2, Fecha: "11/09/2026"})
			last, _ = store.GetLastOrder(from)
			if len(last.Items) != 0 {
				t.Errorf("el pedido de un color arrastró las líneas del anterior: %+v", last.Items)
			}
			if li := last.ItemsDelPedido(); len(li) != 1 || li[0].Color != "AZUL" || li[0].Cantidad != 2 {
				t.Errorf("la vista única del pedido de un color falla: %+v", li)
			}
		})
	}
}

// La ficha del pedido en curso también persiste sus líneas en SQLite (mem las guarda tal cual).
func TestPedidoEnCursoMulticolorEnSQLite(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "bot.db"), 15)
	if err != nil {
		t.Fatalf("abrir sqlite: %v", err)
	}
	const from = "593999000083"
	store.SetPedidoEnCurso(from, PedidoEnCurso{
		Color: "AMARILLO", Cantidad: 1, Flujo: FlujoInmediato,
		Items: []ItemPedido{{Color: "BLANCO", Cantidad: 2}},
	})
	p, hay := store.GetPedidoEnCurso(from)
	if !hay {
		t.Fatal("la ficha no se recuperó")
	}
	if lineas := p.Lineas(); len(lineas) != 2 || lineas[0].Color != "BLANCO" || lineas[0].Cantidad != 2 {
		t.Errorf("las líneas de la ficha se perdieron en SQLite: %+v", p.Lineas())
	}
	if !p.Completo() {
		t.Errorf("una ficha con todas sus cantidades debía estar completa: %+v", p)
	}
}
