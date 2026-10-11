package agent

// Agregador de actividad (feature 049). Recibe muestras instantáneas y acumula,
// en segundos, un resumen por usuario/día/hora. Es puro (sin SO) y serializable,
// para persistir el día en curso entre reinicios del servicio y reanudar la suma.
//
// Produce el MISMO formato JSON que consumen los filtros de reporte de Fase 1
// (actividad_dia / actividad_rango), así que esos reportes sirven sin cambios.

import (
	"sort"
	"time"

	rmm "github.com/braincorp-cl/observer-agent-dist/shared"
)

const (
	topProcsResumen   = 25
	topHoraResumen    = 5
	topTitulosResumen = 10
)

type horaAcc struct {
	A int            `json:"a"`
	I int            `json:"i"`
	P map[string]int `json:"p"`
}

type usuarioAcc struct {
	Primero  int64               `json:"primero"` // epoch
	Ultimo   int64               `json:"ultimo"`
	Activo   int                 `json:"activo"` // segundos
	Inactivo int                 `json:"inactivo"`
	Capturas int                 `json:"capturas"`
	Horas    map[string]*horaAcc `json:"horas"`
	Procs    map[string]int      `json:"procs"`
	Titulos  map[string]int      `json:"titulos"`
}

// AcumuladorActividad es el estado agregado persistible del día(s) en curso.
type AcumuladorActividad struct {
	Equipo string `json:"equipo"`
	SO     string `json:"so"`
	TZ     string `json:"tz"`
	// Dias: fecha "AAAA-MM-DD" -> usuario -> acumulado.
	Dias map[string]map[string]*usuarioAcc `json:"dias"`
}

func nuevoAcumulador(equipo, so, tz string) *AcumuladorActividad {
	return &AcumuladorActividad{Equipo: equipo, SO: so, TZ: tz, Dias: map[string]map[string]*usuarioAcc{}}
}

// Agregar suma una muestra ya evaluada (activo/inactivo decidido por el ciclo).
func (ac *AcumuladorActividad) Agregar(ts time.Time, usuario, proceso, titulo string, activo bool, intervaloSeg int) {
	if intervaloSeg <= 0 {
		return
	}
	if ac.Dias == nil {
		ac.Dias = map[string]map[string]*usuarioAcc{}
	}
	fecha := ts.Format("2006-01-02")
	hh := ts.Format("15")
	du := ac.Dias[fecha]
	if du == nil {
		du = map[string]*usuarioAcc{}
		ac.Dias[fecha] = du
	}
	u := du[usuario]
	if u == nil {
		u = &usuarioAcc{Primero: ts.Unix(), Ultimo: ts.Unix(), Horas: map[string]*horaAcc{}, Procs: map[string]int{}, Titulos: map[string]int{}}
		du[usuario] = u
	}
	if ts.Unix() < u.Primero {
		u.Primero = ts.Unix()
	}
	if ts.Unix() > u.Ultimo {
		u.Ultimo = ts.Unix()
	}
	h := u.Horas[hh]
	if h == nil {
		h = &horaAcc{P: map[string]int{}}
		u.Horas[hh] = h
	}
	if activo {
		u.Activo += intervaloSeg
		h.A += intervaloSeg
		if proceso != "" {
			h.P[proceso] += intervaloSeg
			u.Procs[proceso] += intervaloSeg
		}
		if titulo != "" {
			u.Titulos[titulo] += intervaloSeg
		}
	} else {
		u.Inactivo += intervaloSeg
		h.I += intervaloSeg
	}
}

// SumarCaptura registra que se guardó una captura para ese usuario/día.
func (ac *AcumuladorActividad) SumarCaptura(ts time.Time, usuario string) {
	fecha := ts.Format("2006-01-02")
	if du := ac.Dias[fecha]; du != nil {
		if u := du[usuario]; u != nil {
			u.Capturas++
		}
	}
}

// ResumenDe produce el documento de una fecha en el formato de los reportes.
// Devuelve (resumen, true) sólo si esa fecha tiene datos.
func (ac *AcumuladorActividad) ResumenDe(fecha string, parcial bool) (rmm.ActividadResumen, bool) {
	du := ac.Dias[fecha]
	if len(du) == 0 {
		return rmm.ActividadResumen{}, false
	}
	dia := rmm.ActividadDia{Fecha: fecha, TZ: ac.TZ, Parcial: parcial}
	usuarios := make([]string, 0, len(du))
	for n := range du {
		usuarios = append(usuarios, n)
	}
	sort.Strings(usuarios)
	for _, n := range usuarios {
		u := du[n]
		horas := map[string]rmm.ActividadHora{}
		for hh, h := range u.Horas {
			horas[hh] = rmm.ActividadHora{A: redondearMin(h.A), I: redondearMin(h.I), P: topMapMin(h.P, topHoraResumen)}
		}
		dia.Usuarios = append(dia.Usuarios, rmm.ActividadUsuario{
			Usuario:     n,
			Primero:     time.Unix(u.Primero, 0).Format("15:04"),
			Ultimo:      time.Unix(u.Ultimo, 0).Format("15:04"),
			ActivoMin:   redondearMin(u.Activo),
			InactivoMin: redondearMin(u.Inactivo),
			HuecosMin:   0, // el servicio no tiene huecos por diseño
			Capturas:    u.Capturas,
			Horas:       horas,
			Procs:       topMapMin(u.Procs, topProcsResumen),
			TitulosTop:  topTitulos(u.Titulos, topTitulosResumen),
		})
	}
	return rmm.ActividadResumen{V: 1, SO: ac.SO, Equipo: ac.Equipo, Dias: []rmm.ActividadDia{dia}}, true
}

// PurgarAnteriores elimina del acumulador los días estrictamente anteriores a
// `fecha` (ya cerrados y subidos), para que el estado en disco no crezca.
func (ac *AcumuladorActividad) PurgarAnteriores(fecha string) {
	for f := range ac.Dias {
		if f < fecha {
			delete(ac.Dias, f)
		}
	}
}

func redondearMin(seg int) int {
	return (seg + 30) / 60
}

func topMapMin(m map[string]int, n int) map[string]int {
	type kv struct {
		k string
		v int
	}
	arr := make([]kv, 0, len(m))
	for k, v := range m {
		arr = append(arr, kv{k, v})
	}
	sort.Slice(arr, func(i, j int) bool {
		if arr[i].v != arr[j].v {
			return arr[i].v > arr[j].v
		}
		return arr[i].k < arr[j].k
	})
	out := map[string]int{}
	for i := 0; i < len(arr) && i < n; i++ {
		out[arr[i].k] = redondearMin(arr[i].v)
	}
	return out
}

func topTitulos(m map[string]int, n int) [][]interface{} {
	type kv struct {
		k string
		v int
	}
	arr := make([]kv, 0, len(m))
	for k, v := range m {
		arr = append(arr, kv{k, v})
	}
	sort.Slice(arr, func(i, j int) bool {
		if arr[i].v != arr[j].v {
			return arr[i].v > arr[j].v
		}
		return arr[i].k < arr[j].k
	})
	out := [][]interface{}{}
	for i := 0; i < len(arr) && i < n; i++ {
		out = append(out, []interface{}{arr[i].k, redondearMin(arr[i].v)})
	}
	return out
}
