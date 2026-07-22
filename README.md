# Observer RMM Agent

Agente multiplataforma de la plataforma **Observer RMM** de BrainCorp (Windows,
Linux y macOS · amd64/arm64/386/arm). Se comunica con el backend por NATS y habilita
monitoreo, ejecución de scripts y tareas, inventario de hardware/software y control
remoto (vía MeshCentral).

- **Versión actual:** v2.10.8
- **Releases y binarios:** https://github.com/braincorp-cl/observer-agent-dist/releases
- **Distribución (CDN):** https://agents.observer.cl
- **Plataforma / consola:** https://github.com/braincorp-cl/observer-rmm-dist

## Instalación

El agente se instala desde la consola de Observer RMM (**Agentes → Agregar**), que
genera el instalador con el token de enrolamiento del cliente/sitio. No se instala
manualmente pegando binarios: el instalador registra el equipo y configura el
servicio del sistema y el agente Mesh.

## Compilar el agente

```
env CGO_ENABLED=0 GOOS=<GOOS> GOARCH=<GOARCH> go build -ldflags "-s -w"
```

Licencia: uso interno BrainCorp.
