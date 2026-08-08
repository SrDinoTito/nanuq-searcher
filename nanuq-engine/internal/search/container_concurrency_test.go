package search

// Tests de concurrencia del ResultContainer (TASK: stress con detector de
// carreras). El container es el único punto de escritura compartido entre
// las goroutines de engines (DSG-005): Extend corre en paralelo mientras los
// accessors se leen (el mutex del container refleja el RLock de searx/
// results.py L53). Estos tests replican ese patrón a máxima presión y
// verifican que no hay data races, pérdidas de resultados por condiciones de
// carrera, ni duplicados de engine en los merges (set-union, searx/results.py
// L58-62).

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"nanuq-engine/internal/result"
)

const (
	// concurrencyNumEngines: goroutines escritoras, una por engine fake.
	concurrencyNumEngines = 32
	// concurrencySharedURLs: pool de URLs que los engines comparten para
	// forzar merges concurrentes sobre la misma clave de mainResultsMap.
	concurrencySharedURLs = 8
	// concurrencySharedPer: resultados main por goroutine que apuntan al
	// pool compartido (los demás kinds de Extend se cubren también).
	concurrencySharedPer = 4
)

// extenderPayloads construye los resultados que una goroutine de engine
// entrega a Extend: concurrencySharedPer resultados main sobre URLs del pool
// compartido (fuerzan merges concurrentes con otras goroutines), un resultado
// main con URL única propia (verifica que ninguna entrada se pierde), y un
// resultado de cada kind auxiliar (answer, correction, suggestion, infobox,
// engineData) para estresar todas las ramas del switch de Extend.
func extenderPayloads(engineName string, i int) []*result.RawResult {
	raws := make([]*result.RawResult, 0, concurrencySharedPer+1+5)
	for j := 0; j < concurrencySharedPer; j++ {
		k := (i + j) % concurrencySharedURLs
		raws = append(raws, fakeMain(
			engineName,
			fmt.Sprintf("https://shared.example/%d", k),
			fmt.Sprintf("shared %d by %s", k, engineName),
		))
	}
	raws = append(raws, fakeMain(
		engineName,
		fmt.Sprintf("https://unique.example/%s", engineName),
		fmt.Sprintf("unique %s", engineName),
	))
	raws = append(raws, result.NewAnswer(&result.AnswerSet{
		Answers: []result.Answer{{Title: "A", Content: engineName}},
	}))
	raws = append(raws, result.NewCorrection("correction-"+engineName))
	raws = append(raws, result.NewSuggestion("suggestion-"+engineName))
	raws = append(raws, result.NewInfobox(&result.Infobox{Title: "IB", Content: engineName}))
	raws = append(raws, result.NewEngineData(engineName, map[string]any{"n": i}))
	return raws
}

// wantEnginesForSharedURL calcula el set-union esperado de engines para una
// URL compartida k: la contribuye toda goroutine i con (i+j)%8 == k para
// j∈[0,4) → 4 residuos distintos (mod 8) × 4 goroutines por residuo = 16
// engines distintos, cada uno exactamente una vez.
func wantEnginesForSharedURL(k int) map[string]bool {
	want := make(map[string]bool)
	for i := 0; i < concurrencyNumEngines; i++ {
		for j := 0; j < concurrencySharedPer; j++ {
			if (i+j)%concurrencySharedURLs == k {
				want[fmt.Sprintf("engine-%02d", i)] = true
			}
		}
	}
	return want
}

// TestResultContainerConcurrencyExtend lanza concurrencyNumEngines goroutines
// llamando Extend de forma concurrente con URLs solapadas (merges paralelos)
// y verifica al terminar: el conteo final exacto de entradas (sin pérdidas),
// el set-union de Engines sin duplicados en cada resultado compartido, los
// conteos de los kinds auxiliares, y que Close/GetOrderedResults/Reset se
// comportan correctamente post-wait (el contrato del container: los accessors
// de ordenación corren después de que los engines terminaron). Ejecutar con
// -race: cualquier data race o pérdida de actualización falla el test.
func TestResultContainerConcurrencyExtend(t *testing.T) {
	c := NewResultContainer()

	var wg sync.WaitGroup
	for i := 0; i < concurrencyNumEngines; i++ {
		engineName := fmt.Sprintf("engine-%02d", i)
		wg.Add(1)
		go func(name string, idx int) {
			defer wg.Done()
			c.Extend(name, extenderPayloads(name, idx))
		}(engineName, i)
	}
	wg.Wait()

	// 1. Sin pérdidas: 8 URLs compartidas + 32 URLs únicas. Si una condición
	// de carrera solapara escrituras sobre la misma clave, una entrada podría
	// perderse (un Extend pisa el resultado de otro sin merge) y el conteo
	// bajaría.
	if got, want := len(c.mainResultsMap), concurrencySharedURLs+concurrencyNumEngines; got != want {
		t.Fatalf("mainResultsMap tiene %d entradas, esperaba %d (pérdida de resultados por carrera)", got, want)
	}

	// 2. Set-union sin duplicados: cada URL compartida acumula exactamente
	// los engines que la contribuyeron, una sola vez cada uno.
	for k := 0; k < concurrencySharedURLs; k++ {
		url := fmt.Sprintf("https://shared.example/%d", k)
		mr, ok := c.mainResultsMap[url]
		if !ok {
			t.Fatalf("URL compartida %s ausente del map", url)
		}
		want := wantEnginesForSharedURL(k)
		if len(mr.Engines) != len(want) {
			t.Errorf("URL %s: Engines = %v (len %d), esperaba set-union de %d engines sin duplicados", url, mr.Engines, len(mr.Engines), len(want))
			continue
		}
		for _, e := range mr.Engines {
			if !want[e] {
				t.Errorf("URL %s: engine inesperado %q en Engines %v", url, e, mr.Engines)
			}
		}
	}

	// 3. URLs únicas: cada una pertenece a un solo engine.
	for i := 0; i < concurrencyNumEngines; i++ {
		name := fmt.Sprintf("engine-%02d", i)
		url := fmt.Sprintf("https://unique.example/%s", name)
		mr, ok := c.mainResultsMap[url]
		if !ok {
			t.Fatalf("URL única %s ausente del map (perdida)", url)
		}
		if len(mr.Engines) != 1 || mr.Engines[0] != name {
			t.Errorf("URL única %s: Engines = %v, esperaba [%s]", url, mr.Engines, name)
		}
	}

	// 4. Kinds auxiliares: una contribución por goroutine en cada colección.
	if got, want := len(c.answers), concurrencyNumEngines; got != want {
		t.Errorf("answers = %d, esperaba %d", got, want)
	}
	if got, want := len(c.corrections), concurrencyNumEngines; got != want {
		t.Errorf("corrections = %d, esperaba %d", got, want)
	}
	if got, want := len(c.suggestions), concurrencyNumEngines; got != want {
		t.Errorf("suggestions = %d, esperaba %d", got, want)
	}
	if got, want := len(c.infoboxes), concurrencyNumEngines; got != want {
		t.Errorf("infoboxes = %d, esperaba %d", got, want)
	}
	if got, want := len(c.engineData), concurrencyNumEngines; got != want {
		t.Errorf("engineData = %d, esperaba %d", got, want)
	}

	// 5. Post-wait (contrato real del container, results.py L53): Close,
	// GetOrderedResults y Reset corren tras el wait de las goroutines de
	// engines; GetOrderedResults no elimina resultados, solo los ordena.
	c.Close(1.0, nil)
	if got, want := len(c.GetOrderedResults()), concurrencySharedURLs+concurrencyNumEngines; got != want {
		t.Errorf("GetOrderedResults = %d, esperaba %d", got, want)
	}
	c.Reset()
	if got := len(c.mainResultsMap); got != 0 {
		t.Errorf("Reset no vació mainResultsMap (%d entradas)", got)
	}
}

// TestResultContainerConcurrencyReadWrite estresa el container con lectores
// y escritores simultáneos: goroutines escritoras llamando Extend (con URLs
// únicas por goroutine, de modo que ningún MainResult se muta por merge
// mientras los lectores lo leen — el contrato real es que Close/
// GetOrderedResults corren post-wait) más AddTiming/AddUnresponsiveEngine/
// SetRedirectURL, mientras goroutines lectoras consultan todos los accessors
// incluido GetOrderedResults. Con -race esto valida que el lock+copy pattern
// de los accessors (results.py L53 RLock) protege toda la superficie pública
// bajo presión; además verifica conteos exactos post-wait.
func TestResultContainerConcurrencyReadWrite(t *testing.T) {
	const (
		writers = 16
		readers = 8
		iters   = 100
	)

	c := NewResultContainer()

	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			name := fmt.Sprintf("rw-%02d", w)
			for it := 0; it < iters; it++ {
				c.Extend(name, []*result.RawResult{
					fakeMain(name, fmt.Sprintf("https://rw.example/%02d/%d", w, it), "t"),
					result.NewAnswer(&result.AnswerSet{Answers: []result.Answer{{Title: "A"}}}),
					result.NewCorrection("c"),
					result.NewSuggestion("s"),
				})
				c.AddTiming(name, time.Millisecond)
				c.AddUnresponsiveEngine("peer-"+name, "reason")
				c.SetRedirectURL("https://redirect.example")
			}
		}(w)
	}
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for it := 0; it < iters; it++ {
				_ = c.GetOrderedResults()
				_ = c.Answers()
				_ = c.Corrections()
				_ = c.Suggestions()
				_ = c.Infoboxes()
				_ = c.Timings()
				_ = c.Unresponsive()
				_ = c.RedirectURL()
			}
		}()
	}
	wg.Wait()

	// Post-wait: conteos exactos — ninguna escritura debe perderse.
	if got, want := len(c.GetOrderedResults()), writers*iters; got != want {
		t.Errorf("GetOrderedResults = %d, esperaba %d (resultados perdidos)", got, want)
	}
	if got, want := len(c.Timings()), writers*iters; got != want {
		t.Errorf("Timings = %d, esperaba %d", got, want)
	}
	if got, want := len(c.Unresponsive()), writers*iters; got != want {
		t.Errorf("Unresponsive = %d, esperaba %d", got, want)
	}
	if got, want := len(c.answers), writers*iters; got != want {
		t.Errorf("answers = %d, esperaba %d", got, want)
	}
}
