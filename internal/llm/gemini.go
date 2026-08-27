package llm

import (
	"context"
	"strings"

	"google.golang.org/genai"
)

// geminiProvider es el camino de siempre: una capa fina sobre el SDK de Google, sin cambios
// de comportamiento respecto a lo que el bot venía haciendo.
type geminiProvider struct {
	client *genai.Client
	modelo string
}

// NewGemini crea el proveedor de Gemini.
func NewGemini(ctx context.Context, apiKey, modelo string) (Provider, error) {
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, err
	}
	return &geminiProvider{client: client, modelo: modelo}, nil
}

func (g *geminiProvider) Nombre() string { return "gemini" }
func (g *geminiProvider) Modelo() string { return g.modelo }

func (g *geminiProvider) Generate(ctx context.Context, system System, history []*genai.Content, tools []*genai.Tool) (Response, error) {
	cfg := &genai.GenerateContentConfig{
		// Gemini recibe el prompt de una pieza, exactamente como antes de partirlo en dos.
		SystemInstruction: &genai.Content{Parts: []*genai.Part{{Text: system.Completo()}}},
		Tools:             tools,
	}
	resp, err := g.client.Models.GenerateContent(ctx, g.modelo, history, cfg)
	if err != nil {
		return Response{}, err
	}
	if len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil {
		// Sin candidato no hay turno: el agente lo interpreta como "nada que decir" y corta.
		return Response{}, nil
	}
	cand := resp.Candidates[0]
	var calls []*genai.FunctionCall
	for _, p := range cand.Content.Parts {
		if p != nil && p.FunctionCall != nil {
			calls = append(calls, p.FunctionCall)
		}
	}
	return Response{
		Text:    strings.TrimSpace(resp.Text()),
		Calls:   calls,
		Content: cand.Content,
	}, nil
}
