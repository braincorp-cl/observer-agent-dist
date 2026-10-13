//go:build !(windows && 386)

package agent

import (
	"database/sql"
	"net/url"
	"path/filepath"
	"runtime"
	"time"

	_ "modernc.org/sqlite"
)

// Época de Chromium: microsegundos desde 1601-01-01 UTC.
const chromiumEpochSeg = 11644473600

// leerVisitas lee las visitas posteriores a `desde`, de la más reciente a la
// más antigua. `immutable=1` lee el archivo sin tomar bloqueos: el navegador
// abierto lo tiene bloqueado y una copia por muestra costaría megas de disco.
func leerVisitas(archivo string, tipo tipoHistorial, desde time.Time) []visitaNav {
	p := filepath.ToSlash(archivo)
	if runtime.GOOS == "windows" {
		p = "/" + p
	}
	dsn := (&url.URL{Scheme: "file", Path: p, RawQuery: "mode=ro&immutable=1"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	var q string
	var limite int64
	if tipo == histFirefox {
		q = `SELECT p.url, COALESCE(p.title,''), v.visit_date FROM moz_historyvisits v
			JOIN moz_places p ON p.id = v.place_id WHERE v.visit_date > ?
			ORDER BY v.visit_date DESC LIMIT 300`
		limite = desde.UnixMicro()
	} else {
		q = `SELECT u.url, COALESCE(u.title,''), v.visit_time FROM visits v
			JOIN urls u ON u.id = v.url WHERE v.visit_time > ?
			ORDER BY v.visit_time DESC LIMIT 300`
		limite = (desde.Unix() + chromiumEpochSeg) * 1_000_000
	}
	rows, err := db.Query(q, limite)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []visitaNav
	for rows.Next() {
		var v visitaNav
		var t int64
		if rows.Scan(&v.url, &v.titulo, &t) != nil {
			continue
		}
		if tipo == histFirefox {
			v.ts = time.UnixMicro(t)
		} else {
			v.ts = time.UnixMicro(t - chromiumEpochSeg*1_000_000)
		}
		out = append(out, v)
	}
	return out
}
