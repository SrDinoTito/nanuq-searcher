// gob.go — helpers gob de serialización para ExpireCache.
//
// TASK-015 / REQ-014 / DSG-011: gob (encoding/gob) como serializador de
// valores de la cache, port de pickle de ExpireCacheSQLite (cache.py).
// A diferencia de pickle, gob es estricto en tipos: el valor debe ser
// gob-encodable (tipos built-in, structs, mapas, slices con campos
// exportados). Los tipos concretos no built-in deben registrarse con
// gob.Register (p. ej. en el init del paquete llamador) para poder
// round-trippear a través de la interfaz any que devuelve Get — contrato
// estándar de gob, documentado para TASK-014 (image_proxy).
package cache

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"reflect"
)

// encodeValue serializa v con encoding/gob.
//
// nil se maneja de forma explícita: se codifica como payload vacío
// ([]byte de longitud 0). Un payload vacío no puede colisionar con un
// valor gob real, pues gob siempre emite al menos un byte de cabecera.
func encodeValue(v any) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	var buf bytes.Buffer
	// Idioma gob documentado para round-trips a través de interfaces: se
	// codifica el valor con tipo raíz interfaz (EncodeValue sobre el Elem de
	// &v), no con tipo concreto. Si se codificara el tipo concreto, gob
	// fallaría al decodificar en *any ("can only be decoded from remote
	// interface type"). Verificado con scratch test en el desarrollo.
	enc := gob.NewEncoder(&buf)
	if err := enc.EncodeValue(reflect.ValueOf(&v).Elem()); err != nil {
		return nil, fmt.Errorf("cache: encodeValue: %w", err)
	}
	return buf.Bytes(), nil
}

// decodeValue deserializa b en v (que debe ser un puntero no nil al tipo
// destino, p. ej. &dest).
//
// Un payload vacío (nil codificado) deja v intacto: si v es *any, v queda
// nil. Port del manejo de None de pickle.
func decodeValue(b []byte, v any) error {
	if len(b) == 0 {
		return nil
	}
	if err := gob.NewDecoder(bytes.NewReader(b)).Decode(v); err != nil {
		return fmt.Errorf("cache: decodeValue: %w", err)
	}
	return nil
}
