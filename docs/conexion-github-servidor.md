# Cómo un bot se conecta a GitHub y despliega en el servidor

Guía para que un asistente de código (una sesión de Claude Code, Codex, u otro bot)
pueda instalar cambios de `bot-marangatu-facturas` en producción. Describe exactamente
lo que hace la sesión que viene trabajando en este repo.

La idea central: **el bot nunca toca el servidor**. Trabaja solo contra GitHub. El
despliegue a Oracle lo hace el job `deploy` de GitHub Actions con credenciales que viven
en el repositorio, no en el bot. Un bot nuevo necesita acceso al repositorio y nada más.

```
  bot                  GitHub                              Oracle (hermes-vm)
  ───                  ──────                              ──────────────────
  rama claude/<x>  ──► PR ──► CI "tests" ──► squash-merge
                                               │
                                               ▼
                                     push a main dispara
                                     job "deploy" (Actions)
                                               │  compila bot + metricas
                                               │  tar + SHA256SUMS + REVISION
                                               │
                                               ▼  ssh bot-marangatu-deploy@host deploy
                                                           │
                                                           ▼
                                                 receive.sh → oracle_deploy.py (root)
                                                 verifica, respalda, reemplaza binarios,
                                                 reinicia bot-marangatu.service,
                                                 vuelve atrás si falla
```

## 1. Lo que necesita el bot: acceso a GitHub

Repositorio: `mmallorquin/bot-marangatu-facturas`, rama principal `main`.

Esta sesión se autentica con la **GitHub App de Claude** conectada a la cuenta de Marco:
las credenciales las administra la plataforma; el bot nunca ve un token ni hay nada
guardado en el repo. Para otro bot hay dos caminos equivalentes:

| Opción | Cómo | Alcance mínimo |
|---|---|---|
| Misma GitHub App del bot | Instalarla en el repo desde la configuración del bot (en Claude: *Settings → Connectors → GitHub*, o `https://github.com/apps/claude/installations/select_target`) | Solo este repositorio |
| Token fine-grained | *GitHub → Settings → Developer settings → Fine-grained tokens*, limitado a este repo, vencimiento corto | `Contents: write`, `Pull requests: write`, `Actions: read`, `Metadata: read` |

Con eso el bot puede: clonar, crear ramas, hacer push, abrir PR, leer el estado de CI y
mergear. **No** le da acceso a los secrets del entorno `production` (ni hace falta).

## 2. Reglas de trabajo

1. **Una rama por bot/sesión**, con prefijo: `claude/<nombre>`. Nunca push directo a `main`.
2. Antes de cada tanda de cambios, arrancar desde `main` actualizado.
3. Verificar en local lo mismo que verifica CI: `gofmt`, `go vet`, `go test -race`.
4. Commit con mensaje descriptivo en español, y al final las líneas de atribución del bot
   (por ejemplo `Co-Authored-By: ...` y el enlace a la sesión).
5. Abrir un PR a `main` con una descripción de qué cambia y qué tests lo cubren.
6. Esperar el check `tests` en verde. **Nunca mergear en rojo, nunca desactivar un test.**
7. Squash-merge con título `<mensaje del PR> (#N)`.
8. Confirmar que el job `deploy` del run de `main` terminó en `success` (sección 5).

Nunca commitear: `.env`, tokens de Telegram u OpenRouter, claves SSH, fotos de facturas,
bases SQLite. `.gitignore` ya cubre lo habitual; igual revisar `git status` antes del commit.

## 3. Comandos

```bash
# arrancar desde main
git fetch origin main
git checkout -B claude/<nombre> origin/main

# ... cambios ...

gofmt -l .            # tiene que no imprimir nada
go vet ./...
go test -race ./...

git add -A
git commit -m "feat: ..."      # con las líneas de atribución al final
git push -u origin claude/<nombre>
```

Caso especial: si la rama remota ya fue squash-mergeada en un PR anterior, `git push`
normal falla por "non-fast-forward". Es seguro forzar **solo** si la rama remota no tiene
nada sin mergear:

```bash
git fetch origin
git merge-base --is-ancestor origin/claude/<nombre> origin/main \
  || git diff --quiet origin/main origin/claude/<nombre>     # sin diferencias de contenido
git push --force-with-lease -u origin claude/<nombre>
```

Si cualquiera de las dos comprobaciones falla, hay commits sin mergear: no forzar, rebasar.

### API de GitHub (lo que usa esta sesión)

Con un cliente REST autenticado (`gh api`, `curl -H "Authorization: Bearer $TOKEN"`, o las
herramientas MCP de GitHub):

| Para | Endpoint |
|---|---|
| Abrir PR | `POST /repos/{owner}/{repo}/pulls` con `head`, `base: main`, `title`, `body` |
| Estado de CI del commit | `GET /repos/{owner}/{repo}/commits/{sha}/check-runs` → cada `check_runs[].conclusion` |
| Estado del PR | `GET /repos/{owner}/{repo}/pulls/{n}` → `mergeable_state == "clean"` |
| Mergear | `PUT /repos/{owner}/{repo}/pulls/{n}/merge` con `{"merge_method":"squash","commit_title":"... (#n)"}` |
| Runs de `main` | `GET /repos/{owner}/{repo}/actions/runs?branch=main&per_page=1` |
| Jobs de un run | `GET /repos/{owner}/{repo}/actions/runs/{id}/jobs` → buscar `deploy` |
| Logs de un job fallido | `GET /repos/{owner}/{repo}/actions/jobs/{job_id}/logs` |

En un PR, el workflow corre solo el job `tests`; `deploy` aparece como `skipped`. Eso es
lo esperado: el deploy ocurre recién con el push a `main`.

## 4. Qué pasa después del merge (el servidor)

Definido en [`.github/workflows/tests.yml`](../.github/workflows/tests.yml):

1. `tests` corre otra vez sobre `main` (gofmt, vet, tests Go, tests del receptor en Python,
   `sh -n` de los scripts).
2. `deploy` corre solo si `github.ref == refs/heads/main` y el evento es `push` o
   `workflow_dispatch`, con el entorno **production** (admite únicamente la rama `main`)
   y un grupo de concurrencia para que no haya dos deploys a la vez.
3. Compila `cmd/bot` y `cmd/metricas` para `linux/amd64` con `CGO_ENABLED=0`, escribe
   `REVISION` (el SHA) y `SHA256SUMS`, y arma `release.tar`.
4. Si `main` ya apunta a un commit más nuevo, **omite** este deploy (lo cubre el siguiente).
5. Abre una conexión SSH como `bot-marangatu-deploy` y manda el tar por stdin con el
   comando `deploy`.

Lo que vive en el entorno `production` del repo (nombres, nunca valores):

| Nombre | Tipo | Uso |
|---|---|---|
| `ORACLE_HOST` | Variable | Dirección del servidor |
| `ORACLE_PORT` | Variable | Puerto SSH |
| `ORACLE_USER` | Variable | `bot-marangatu-deploy` |
| `ORACLE_SSH_KEY` | Secret | Clave privada exclusiva de despliegue |
| `ORACLE_KNOWN_HOSTS` | Secret | Huella del servidor, verificada |

Del lado del servidor (instalado una sola vez con acceso administrativo, ver
[`deploy-oracle.md`](deploy-oracle.md)):

- La clave de deploy está en `authorized_keys` con `restrict,command="…/bot-marangatu-receive"`:
  sin shell, sin SCP, sin PTY, sin forwarding. [`deploy/receive.sh`](../deploy/receive.sh)
  solo acepta `deploy` y `status`.
- [`deploy/oracle_deploy.py`](../deploy/oracle_deploy.py) corre como root vía sudo
  limitado: valida el tar (solo esos cuatro archivos, tamaños acotados, sin rutas raras),
  comprueba arquitectura, revisión y checksums; detiene el servicio; respalda SQLite y
  los binarios anteriores en `/var/backups/bot-marangatu/`; reemplaza
  `/opt/bot-marangatu/bot` y `/opt/bot-marangatu/metricas` de forma atómica; arranca
  `bot-marangatu.service` y verifica que el proceso nuevo es el que corre. Si algo falla,
  reinstala los binarios anteriores (la base **no** se restaura sola).
- Los tokens de Telegram y OpenRouter están solo en `/etc/bot-marangatu.env` del servidor.

## 5. Cómo verifica el bot que el cambio llegó

```bash
# último run de main
gh api "repos/mmallorquin/bot-marangatu-facturas/actions/runs?branch=main&per_page=1" \
  --jq '.workflow_runs[0] | {head_sha, status, conclusion, html_url}'

# jobs de ese run: tests y deploy tienen que estar en success
gh api "repos/mmallorquin/bot-marangatu-facturas/actions/runs/<id>/jobs" \
  --jq '.jobs[] | {name, status, conclusion}'
```

Criterio de "instalado": el run cuyo `head_sha` es el commit del merge tiene
`deploy.conclusion == "success"`. Después, una prueba funcional desde Telegram (mandar una
foto, usar el botón que cambió).

Si `deploy` falla: leer el log del job. Las causas típicas son un check de arranque que
no confirmó (el receptor ya volvió a la versión anterior, el bot sigue funcionando) o un
secret/variable faltante. El bot informa la causa; la reinstalación del receptor o la
rotación de claves las hace Marco con acceso administrativo.

## 6. Lo que el bot no puede ni debe hacer

- Conectarse por SSH al servidor, ni pedir, guardar o imprimir `ORACLE_SSH_KEY`,
  `ORACLE_KNOWN_HOSTS` o los tokens del bot.
- Modificar `deploy/` esperando que se aplique solo: el workflow reemplaza únicamente los
  dos binarios; cambios al receptor requieren que Marco corra `sudo sh setup-actions.sh`
  en el servidor.
- Mergear con CI en rojo, saltear o desactivar tests, hacer push directo a `main`,
  reescribir historia de una rama que no sea suya.
- Agregar automatizaciones de deploy nuevas (scripts, cron, releases): el flujo actual es
  el único y es deliberadamente simple.

Logs y métricas del servidor se consultan desde la Mac de Marco (`ssh hermes-vm …`,
ver [`deploy-oracle.md`](deploy-oracle.md)); si el bot los necesita, los pide.

## 7. Checklist para habilitar un bot nuevo

1. Darle acceso al repo (sección 1), limitado a `mmallorquin/bot-marangatu-facturas`.
2. Decirle el nombre de su rama (`claude/<nombre>`) y pasarle el prompt de abajo.
3. Primer cambio chico de prueba (por ejemplo una línea en este documento) para ver el
   ciclo completo: rama → PR → `tests` verde → squash-merge → `deploy: success`.
4. Verificar en Telegram que el bot de facturas sigue respondiendo.
5. Si el bot pide credenciales del servidor, la respuesta es no: no las necesita.

### Prompt para el bot nuevo

```
Trabajás sobre el repositorio GitHub mmallorquin/bot-marangatu-facturas (Go). Leé primero
docs/conexion-github-servidor.md y seguilo al pie de la letra.

Reglas:
- Desarrollá en la rama claude/<nombre>, creada desde origin/main. Nunca push a main.
- Antes de commitear: gofmt -l . (vacío), go vet ./..., go test -race ./... en verde.
- Commit en español con las líneas de atribución al final. Nada de .env, tokens, claves ni fotos.
- Abrí un PR a main, esperá el check "tests" en verde y hacé squash-merge con título
  "<título del PR> (#N)". No mergees en rojo ni desactives tests.
- Después del merge, confirmá que el job "deploy" del run de main terminó en success y
  reportá el SHA. Eso es "instalado". No hay otro paso de deploy.
- No tenés ni necesitás acceso SSH al servidor ni a los secrets del entorno production.
  Si algo del servidor falla, leé el log del job y reportá la causa.

Terminado = PR mergeado, deploy en success, y una línea de qué probar en Telegram.
```
