# Notas de release del agente

**Regla: todo release nuevo del agente DEBE traer el detalle de los cambios de la versión.**
Sin `release-notes/<tag>.md` el workflow de release **falla antes de compilar**, así que no es un
recordatorio: es un requisito.

## Cómo publicar una versión

1. Escribir `release-notes/v<X.Y.Z>.md` con el cuerpo **completo** de la release (ver estructura
   abajo) y commitearlo.
2. Taggear ese commit (`git tag -a vX.Y.Z -m "..."`) y pushear el tag al remote `dist`.
3. El workflow valida que el archivo exista, compila, crea la release de GitHub **con este archivo
   como cuerpo** y publica los binarios al CDN.

Si el tag ya se pusheó sin el archivo, el job de validación corta el release: agregar el archivo,
borrar el tag en local y en `dist`, y volver a taggear.

## Estructura del archivo

El archivo es el cuerpo completo de la release, en **español**, escrito para el **operador** (no para
quien escribió el código). La referencia canónica de estilo es
[`v2.10.8`](https://github.com/braincorp-cl/observer-agent-dist/releases/tag/v2.10.8):

1. Línea de presentación del agente + mención al CDN.
2. Si es una corrección sobre la versión anterior, una cita `>` diciéndolo y si conviene actualizar.
3. `## Cambios en esta versión` — viñetas con el **lead-in en negrita**. Qué cambia **para quien usa
   el producto**, no qué archivo se tocó. Los arreglos empiezan con **"Corregido"** y explican qué se
   veía mal antes.
4. `## Plataformas` — la lista de los 8 binarios.
5. `## Instalación` — que se instala desde la consola, no bajando el binario de esta página.
6. Pie con la licencia (uso interno BrainCorp).

## Por qué existe esta carpeta

`generate_release_notes: true` produce un enlace "Full Changelog" y nada más: una lista de mensajes de
commit no le sirve a quien tiene que decidir si actualiza la flota. **Entre `v2.11.0` y `v2.14.2`
—nueve releases— todas salieron con ese stub**; sólo `v2.10.8` tenía cuerpo curado, escrito a mano
después. Tener las notas versionadas acá hace que se revisen en el commit, que no dependan de que
alguien se acuerde de editar la release en la web, y que el gate de CI las pueda exigir.

Las releases anteriores a `v2.14.0` quedaron **sin backfill**: la regla aplica a los releases nuevos.
