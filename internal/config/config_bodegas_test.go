package config

import "testing"

func TestListaDeTelefonos(t *testing.T) {
	m := listaDeTelefonos(" 593999000111, 0999000222 ,, ")
	if len(m) != 2 || !m["593999000111"] || !m["593999000222"] {
		t.Fatalf("%v", m)
	}
	if listaDeTelefonos("") != nil {
		t.Fatal("vacía debe ser nil")
	}
}
