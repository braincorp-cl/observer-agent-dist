package agent

// Agregador de actividad (feature 049). Recibe muestras instantáneas y acumula,
// en segundos, un resumen por usuario/día/hora. Es puro (sin SO) y serializable,
// para persistir el día en curso entre reinicios del servicio y reanudar la suma.
//
// Produce el MISMO formato JSON que consumen los filtros de reporte de Fase 1
// (actividad_dia / actividad_rango), así que esos reportes sirven sin cambios.

import (
	"sort"
	"strconv"
	"time"

	rmm "github.com/braincorp-cl/observer-agent-dist/shared"
)

const (
	topProcsResumen   = 25
	topHoraResumen    = 15 // 049-E: 15 para filtrar por horario laboral con poco error
	topUrlsResumen    = 25
	topTitulosResumen = 10
	// 049-D
	tramosPorDia = 144 // tramos de 10 min
	pausaMinSeg  = 600 // sin entrada 10 min o más = pausa
	maxPausasDia = 60
	maxHuecosDia = 60
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
	// 049-C
	Sitios map[string]int     `json:"sitios,omitempty"`
	Red    map[string]*redAcc `json:"red,omitempty"`
	// 049-D: tramo de 10 min -> [seg activos, seg muestreados]; jornada por
	// entrada real (epoch) y pausas [desde, hasta].
	Tramos        map[int][2]int `json:"tramos,omitempty"`
	PrimerEntrada int64          `json:"primer_entrada,omitempty"`
	UltimaEntrada int64          `json:"ultima_entrada,omitempty"`
	Pausas        [][2]int64     `json:"pausas,omitempty"`
	// 049-E: segundos inactivos con el proceso/sitio/URL en primer plano,
	// segundos por URL activa y conteos de entrada (día y tramo -> [teclas, mouse]).
	ProcsI  map[string]int `json:"procs_i,omitempty"`
	SitiosI map[string]int `json:"sitios_i,omitempty"`
	Urls    map[string]int `json:"urls,omitempty"`
	UrlsI   map[string]int `json:"urls_i,omitempty"`
	Teclas  int            `json:"teclas,omitempty"`
	Mouse   int            `json:"mouse,omitempty"`
	Entrada map[int][2]int `json:"entrada,omitempty"`
}

type huecoAcc struct {
	Desde  int64  `json:"desde"`
	Hasta  int64  `json:"hasta"`
	Motivo string `json:"motivo"`
}

type redAcc struct {
	Seg      int             `json:"seg"`
	ConexMax int             `json:"conex_max"`
	Destinos map[string]bool `json:"destinos"`
}

// AcumuladorActividad es el estado agregado persistible del día(s) en curso.
type AcumuladorActividad struct {
	Equipo string `json:"equipo"`
	SO     string `json:"so"`
	TZ     string `json:"tz"`
	// Dias: fecha "AAAA-MM-DD" -> usuario -> acumulado.
	Dias map[string]map[string]*usuarioAcc `json:"dias"`
	// 049-D: huecos de muestreo por fecha y hora de la última muestra tomada con
	// el monitoreo encendido (epoch; 0 = sin referencia). Persisten para detectar
	// el hueco de un equipo apagado al volver a arrancar.
	Huecos        map[string][]huecoAcc `json:"huecos,omitempty"`
	UltimaMuestra int64                 `json:"ultima_muestra,omitempty"`
}

func tramoDe(ts time.Time) int {
	return ts.Hour()*6 + ts.Minute()/10
}

func inicioDelDia(ts time.Time) time.Time {
	y, m, d := ts.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, ts.Location())
}

func restar(m map[string]int, k string, v int) {
	if m == nil || k == "" {
		return
	}
	if m[k] -= v; m[k] <= 0 {
		delete(m, k)
	}
}

func sumar(m *map[string]int, k string, v int) {
	if k == "" || v <= 0 {
		return
	}
	if *m == nil {
		*m = map[string]int{}
	}
	(*m)[k] += v
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
	if u.Tramos == nil {
		u.Tramos = map[int][2]int{}
	}
	tr := u.Tramos[tramoDe(ts)]
	tr[1] += intervaloSeg
	if activo {
		tr[0] += intervaloSeg
	}
	u.Tramos[tramoDe(ts)] = tr
	if activo {
		u.Activo += intervaloSeg
		h.A += intervaloSeg
		if proceso != "" {
			h.P[proceso] += intervaloSeg
			u.Procs[proceso] += intervaloSeg
		}
		if titulo != "" {
			u.Titulos[titulo] += intervaloSeg
			if sitio := sitioDeTitulo(proceso, titulo); sitio != "" {
				if u.Sitios == nil {
					u.Sitios = map[string]int{}
				}
				u.Sitios[sitio] += intervaloSeg
			}
		}
	} else {
		u.Inactivo += intervaloSeg
		h.I += intervaloSeg
		// 049-E: el tiempo sin uso también se atribuye a la app que quedó al
		// frente (el ciclo pasa proceso "" con la sesión bloqueada).
		if procesoReal(proceso) {
			sumar(&u.ProcsI, proceso, intervaloSeg)
			if titulo != "" {
				sumar(&u.SitiosI, sitioDeTitulo(proceso, titulo), intervaloSeg)
			}
		}
	}
}

func (ac *AcumuladorActividad) usuarioDe(ts time.Time, usuario string) *usuarioAcc {
	return ac.Dias[ts.Format("2006-01-02")][usuario]
}

// AgregarURL suma la URL de la pestaña activa (049-E). Va aparte de Agregar
// porque sólo existe con un navegador al frente; se llama DESPUÉS de Agregar.
func (ac *AcumuladorActividad) AgregarURL(ts time.Time, usuario, url string, activo bool, intervaloSeg int) {
	u := ac.usuarioDe(ts, usuario)
	if u == nil || url == "" {
		return
	}
	if activo {
		sumar(&u.Urls, url, intervaloSeg)
	} else {
		sumar(&u.UrlsI, url, intervaloSeg)
	}
}

// ReclasificarURL mueve la URL de una muestra re-etiquetada de activa a inactiva.
func (ac *AcumuladorActividad) ReclasificarURL(ts time.Time, usuario, url string, intervaloSeg int) {
	u := ac.usuarioDe(ts, usuario)
	if u == nil || url == "" || u.Urls[url] <= 0 {
		return
	}
	seg := intervaloSeg
	if seg > u.Urls[url] {
		seg = u.Urls[url]
	}
	restar(u.Urls, url, seg)
	sumar(&u.UrlsI, url, seg)
}

// AgregarEntrada suma teclas y eventos de mouse ocurridos desde la muestra
// anterior (049-E). Sólo conteos: el agente nunca registra qué tecla fue.
func (ac *AcumuladorActividad) AgregarEntrada(ts time.Time, usuario string, teclas, mouse int) {
	u := ac.usuarioDe(ts, usuario)
	if u == nil || (teclas <= 0 && mouse <= 0) {
		return
	}
	if teclas < 0 {
		teclas = 0
	}
	if mouse < 0 {
		mouse = 0
	}
	u.Teclas += teclas
	u.Mouse += mouse
	if u.Entrada == nil {
		u.Entrada = map[int][2]int{}
	}
	e := u.Entrada[tramoDe(ts)]
	e[0] += teclas
	e[1] += mouse
	u.Entrada[tramoDe(ts)] = e
}

// Reclasificar pasa a inactiva una muestra que se contó activa (049-D). Lo usa
// el ciclo cuando, al cruzar el umbral de inactividad, se sabe que la última
// entrada fue ANTES de esa muestra: estaba activa sólo porque el idle aún no
// llegaba al umbral. Deshace exactamente lo que Agregar sumó como activo.
func (ac *AcumuladorActividad) Reclasificar(ts time.Time, usuario, proceso, titulo string, intervaloSeg int) {
	u := ac.Dias[ts.Format("2006-01-02")][usuario]
	if u == nil || intervaloSeg <= 0 {
		return
	}
	seg := intervaloSeg
	if seg > u.Activo {
		seg = u.Activo
	}
	if seg <= 0 {
		return
	}
	u.Activo -= seg
	u.Inactivo += seg
	if h := u.Horas[ts.Format("15")]; h != nil {
		h.A -= seg
		if h.A < 0 {
			h.A = 0
		}
		h.I += seg
		restar(h.P, proceso, seg)
	}
	restar(u.Procs, proceso, seg)
	if procesoReal(proceso) {
		sumar(&u.ProcsI, proceso, seg)
	}
	if titulo != "" {
		restar(u.Titulos, titulo, seg)
		sitio := sitioDeTitulo(proceso, titulo)
		restar(u.Sitios, sitio, seg)
		sumar(&u.SitiosI, sitio, seg)
	}
	if tr, ok := u.Tramos[tramoDe(ts)]; ok {
		tr[0] -= seg
		if tr[0] < 0 {
			tr[0] = 0
		}
		u.Tramos[tramoDe(ts)] = tr
	}
}

// RegistrarEntrada anota la hora de la última entrada real del usuario
// (049-D): `ts` es la hora de la muestra y `entrada` = ts − idle. Con eso arma
// la jornada (primera y última entrada) y las pausas: un salto de pausaMinSeg o
// más entre dos entradas consecutivas. No depende de la reclasificación.
func (ac *AcumuladorActividad) RegistrarEntrada(ts time.Time, usuario string, entrada time.Time) {
	u := ac.Dias[ts.Format("2006-01-02")][usuario]
	if u == nil {
		return
	}
	if dia := inicioDelDia(ts); entrada.Before(dia) {
		entrada = dia
	}
	if entrada.After(ts) {
		entrada = ts
	}
	e := entrada.Unix()
	if u.UltimaEntrada > 0 && e-u.UltimaEntrada >= pausaMinSeg && len(u.Pausas) < maxPausasDia {
		u.Pausas = append(u.Pausas, [2]int64{u.UltimaEntrada, e})
	}
	if e > u.UltimaEntrada {
		u.UltimaEntrada = e
	}
	if u.PrimerEntrada == 0 || e < u.PrimerEntrada {
		u.PrimerEntrada = e
	}
}

// RegistrarHueco anota un tramo sin muestreo (049-D). Si cruza la medianoche
// se recorta al día de `hasta`: el día anterior ya se cerró y se subió.
func (ac *AcumuladorActividad) RegistrarHueco(desde, hasta time.Time, motivo string) {
	if dia := inicioDelDia(hasta); desde.Before(dia) {
		desde = dia
	}
	if !hasta.After(desde) {
		return
	}
	if ac.Huecos == nil {
		ac.Huecos = map[string][]huecoAcc{}
	}
	fecha := hasta.Format("2006-01-02")
	if len(ac.Huecos[fecha]) >= maxHuecosDia {
		return
	}
	ac.Huecos[fecha] = append(ac.Huecos[fecha], huecoAcc{Desde: desde.Unix(), Hasta: hasta.Unix(), Motivo: motivo})
}

// AgregarRed suma la foto de red de una muestra al usuario de esa muestra
// (049-C). Se cuenta aunque el usuario esté inactivo: el tráfico de fondo
// también es uso de red.
func (ac *AcumuladorActividad) AgregarRed(ts time.Time, usuario string, foto map[string]appRed, intervaloSeg int) {
	if intervaloSeg <= 0 || len(foto) == 0 {
		return
	}
	du := ac.Dias[ts.Format("2006-01-02")]
	if du == nil {
		return
	}
	u := du[usuario]
	if u == nil {
		return
	}
	if u.Red == nil {
		u.Red = map[string]*redAcc{}
	}
	for app, r := range foto {
		acc := u.Red[app]
		if acc == nil {
			acc = &redAcc{Destinos: map[string]bool{}}
			u.Red[app] = acc
		}
		acc.Seg += intervaloSeg
		if r.Conex > acc.ConexMax {
			acc.ConexMax = r.Conex
		}
		for _, d := range r.Destinos {
			if len(acc.Destinos) >= maxDestinosPorApp {
				break
			}
			acc.Destinos[d] = true
		}
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
			Usuario:      n,
			Primero:      time.Unix(u.Primero, 0).Format("15:04"),
			Ultimo:       time.Unix(u.Ultimo, 0).Format("15:04"),
			ActivoMin:    redondearMin(u.Activo),
			InactivoMin:  redondearMin(u.Inactivo),
			HuecosMin:    0, // el servicio no tiene huecos por diseño
			Capturas:     u.Capturas,
			Horas:        horas,
			Procs:        topMapMin(u.Procs, topProcsResumen),
			TitulosTop:   topTitulos(u.Titulos, topTitulosResumen),
			Sitios:       topMapMin(u.Sitios, topSitiosResumen),
			Red:          resumenRed(u.Red, topRedResumen),
			Tramos:       resumenTramos(u.Tramos),
			PrimerActivo: horaMin(u.PrimerEntrada),
			UltimoActivo: horaMin(u.UltimaEntrada),
			Pausas:       resumenIntervalos(u.Pausas),
			ProcsI:       topMapMin(u.ProcsI, topProcsResumen),
			SitiosI:      topMapMin(u.SitiosI, topSitiosResumen),
			Urls:         topMapMin(u.Urls, topUrlsResumen),
			UrlsI:        topMapMin(u.UrlsI, topUrlsResumen),
			Teclas:       u.Teclas,
			Mouse:        u.Mouse,
			Entrada:      resumenTramos(u.Entrada),
		})
	}
	for _, h := range ac.Huecos[fecha] {
		dia.Huecos = append(dia.Huecos, rmm.ActividadHueco{Desde: horaMin(h.Desde), Hasta: horaMin(h.Hasta), Motivo: h.Motivo})
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
	for f := range ac.Huecos {
		if f < fecha {
			delete(ac.Huecos, f)
		}
	}
}

func horaMin(epoch int64) string {
	if epoch <= 0 {
		return ""
	}
	return time.Unix(epoch, 0).Format("15:04")
}

func resumenTramos(m map[int][2]int) map[string][2]int {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string][2]int, len(m))
	for i, v := range m {
		if i >= 0 && i < tramosPorDia && (v[0] > 0 || v[1] > 0) {
			out[strconv.Itoa(i)] = v
		}
	}
	return out
}

func resumenIntervalos(p [][2]int64) [][2]string {
	if len(p) == 0 {
		return nil
	}
	out := make([][2]string, 0, len(p))
	for _, x := range p {
		out = append(out, [2]string{horaMin(x[0]), horaMin(x[1])})
	}
	return out
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

func resumenRed(m map[string]*redAcc, n int) map[string]rmm.ActividadRedApp {
	if len(m) == 0 {
		return nil
	}
	seg := map[string]int{}
	for k, v := range m {
		seg[k] = v.Seg
	}
	out := map[string]rmm.ActividadRedApp{}
	for app, min := range topMapMin(seg, n) {
		r := m[app]
		out[app] = rmm.ActividadRedApp{Min: min, ConexMax: r.ConexMax, Destinos: len(r.Destinos)}
	}
	return out
}
