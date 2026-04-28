# kube-ctl

Минимальный read-only дашборд для нескольких kubernetes-кластеров в одном
бэкенд-процессе. Go (`client-go` + `metrics.k8s.io`), фронт — один HTML/JS
без сборки.

Источник CPU/MEM — встроенный в кластер `metrics-server` (в k3s он идёт
в коробке). Внешний Prometheus не нужен.

## Быстрый старт

```bash
# 1. убедиться, что metrics-server работает
kubectl top nodes

# 2. запуск
make run
# → http://localhost:8080
```

Переменные:

```bash
make run KUBECONFIG=~/.kube/homelab ADDR=:8080
```

## Проверка на локальном kubectl (Ubuntu)

Если на машине уже настроен `kubectl` (есть `~/.kube/config` и кластер
отвечает), запустить дашборд против него можно без Docker и без деплоя
в кластер.

```bash
# 0. зависимости (один раз)
sudo apt update
sudo apt install -y golang-go make git

# 1. убедиться, что kubectl видит кластер и metrics-server живой
kubectl cluster-info
kubectl get nodes
kubectl top nodes          # если пусто — ставим metrics-server
```

В k3s metrics-server идёт по умолчанию. В обычном k8s — один раз:

```bash
kubectl apply -f https://github.com/kubernetes-sigs/metrics-server/releases/latest/download/components.yaml
```

Дальше:

```bash
git clone <repo-url> k3s-multi-dashboard
cd k3s-multi-dashboard
make run
# → открыть http://localhost:8080
```

Быстрая sanity-проверка API, не открывая фронт:

```bash
curl -s localhost:8080/api/health   | jq
curl -s localhost:8080/api/nodes    | jq
curl -s localhost:8080/api/overview | jq
```

Стаб-режим (без кластера, всё синтетическое):

```bash
make run-stub
# → http://localhost:8080
```

## Endpoints

| Path                    | Описание                                         |
|-------------------------|--------------------------------------------------|
| `GET /api/health`       | k8s + metrics-server доступность                 |
| `GET /api/overview`     | суммарная статистика + avg CPU/MEM               |
| `GET /api/nodes`        | список нод с per-node CPU/MEM                    |
| `GET /api/pods/status`  | подсчёт по phase                                 |
| `GET /api/workloads`    | deployments + statefulsets + daemonsets          |
| `GET /api/events?limit` | последние events (отсортированы по времени)      |
| `GET /api/namespaces`   | список namespace + подсчёт подов                 |
| `GET /api/metrics/cluster` | CPU/MEM time-series за 30 минут (in-memory)   |
| `GET /api/clusters`     | список кластеров + их health                     |
| `GET /api/servers`      | хосты с node-exporter + worst severity + alerts  |
| `GET /api/servers/detail?name=N` | детали одного хоста (uname/cpu/mem/fs/net) |
| `GET /api/runners`      | список GitLab Runners (требует gitlab-url/token) |
| `GET /api/pipelines`   | пайплайны по проектам, сгруппированные и кешированные 30s |
| `GET /api/pipelines/trends?project=<id>` | тренд за 24h по одному проекту |
| `POST /api/pipelines/action` | retry / cancel пайплайна (`{"action","pipeline_id","project_id"}`) |

Все API-эндпоинты принимают необязательный query-параметр `?cluster=<name>`.
Без него — первый в порядке объявления.

## Как считается CPU/MEM

- Per-node: `Usage.Cpu` / `Status.Capacity.Cpu` и `Usage.Memory` / `Capacity.Memory`
  из `metrics.k8s.io/v1beta1` и `corev1.Node`.
- Avg по кластеру: `sum(used) / sum(capacity)` по всем нодам.
- 30-минутный график: бэкенд раз в 60 секунд сэмплирует avg в ring buffer
  (30 точек) на кластер. История живёт в памяти, сбрасывается при рестарте —
  это компромисс за отсутствие Prometheus.

## Несколько кластеров (один backend)

Один процесс умеет держать несколько кластеров — конфиг через YAML:

```yaml
# clusters.yaml
clusters:
  - name: homelab
    kubeconfig: /home/dmitry/.kube/homelab
  - name: prod
    kubeconfig: /home/dmitry/.kube/prod
```

Запуск:

```bash
make run-multi CLUSTERS=./clusters.yaml
# или напрямую:
# ./kube-ctl -clusters=./clusters.yaml -addr=:8080 -static=./frontend
```

В UI в шапке появятся табы с именами кластеров и индикатором статуса:
- зелёный — k8s + metrics-server OK;
- жёлтый — k8s OK, metrics-server недоступен (CPU/MEM будут нулями);
- красный — кластер недоступен.

```bash
curl -s 'localhost:8080/api/clusters'                      # список + health
curl -s 'localhost:8080/api/overview?cluster=prod'  | jq
curl -s 'localhost:8080/api/nodes?cluster=homelab'  | jq
```

Однокластерный shortcut (без YAML) через `-kubeconfig`, создаёт кластер
с именем `default`:

```bash
make run KUBECONFIG=~/.kube/homelab ADDR=:8080
```

## Серверы с node-exporter

Помимо k8s-кластеров, дашборд может опрашивать обычные хосты с
`node_exporter` на `:9100` и показывать их в выпадающем меню `SERVERS`
в правом верхнем углу.

Конфиг — отдельный YAML, передаётся флагом `-servers`:

```yaml
# servers.yaml
servers:
  - name: web-1
    url: http://192.168.10.10:9100
  - name: db-1
    url: http://192.168.10.11:9100
```

Запуск:

```bash
make run-multi CLUSTERS=./clusters.yaml SERVERS=./servers.yaml
# или: make run SERVERS=./servers.yaml (одиночный кластер + серверы)
```

Бэкенд раз в 30s пуллит `/metrics`, парсит prometheus text format и
прогоняет 5 правил из awesome-prometheus-alerts:

| Rule                  | Severity                                  |
|-----------------------|-------------------------------------------|
| HostHighMemoryUsage   | warn ≥ 85%, crit ≥ 95%                    |
| HostHighLoadAverage   | warn ≥ 1.5/core, crit ≥ 2.0/core (load5)  |
| HostOutOfDiskSpace    | warn < 15% free, crit < 5% (любая фс)     |
| HostOutOfInodes       | warn < 15% free, crit < 5% (любая фс)     |
| HostClockSkew         | warn ≥ 50ms, crit ≥ 1s (node_timex_offset)|
| NodeExporterDown      | crit при недоступности /metrics           |

В UI:
- вкладка **Servers** в навигации (рядом с Cluster и GitLab Runners),
  бейдж на вкладке = число хостов с активными алертами; цвет вкладки
  = worst severity по всем серверам.
- На странице — список хостов: точка статуса, имя, URL.
- Клик по строке разворачивает 5 правил инлайн (severity-бейдж + сообщение
  вроде «memory used 88.3%»).
- Кнопка `OPEN ↗` в строке открывает полноэкранную страницу-деталку
  (`#/server/<name>`) с uname, uptime, load avg, memory breakdown,
  filesystems с inode-загрузкой, network counters и всеми алертами.

API:

```bash
curl -s localhost:8080/api/servers | jq
curl -s 'localhost:8080/api/servers/detail?name=web-1' | jq
```

## UI — цветовая система

В интерфейсе используются четыре смысловых цвета. Они применяются к вкладкам
навигации, точкам статуса и бейджам алертов:

| Цвет     | CSS-переменная    | Смысл                                         |
|----------|-------------------|-----------------------------------------------|
| Зелёный  | `--accent`        | OK / online / всё в норме                     |
| Жёлтый   | `--warn`          | Предупреждение / деградация / paused           |
| Красный  | `--err`           | Проблема / недоступен / ошибка                 |
| Фиолетовый | `--purple`      | Статичное / не подключено к live-данным        |

**Вкладки навигации** (`Cluster`, `GitLab Runners`, `Servers`, `Tools`):
- нейтральный стиль в покое, без лишнего цвета;
- получают класс `.ok` / `.warn` / `.crit` / `.down` программно, исходя из
  состояния данных;
- активная вкладка с классом статуса даёт тонированный фон (цвет dim +
  цветной бордер);
- вкладка `Tools` всегда фиолетовая — она не отражает live-статус.

**Точки раннеров** (`.runner-dot`):
- зелёная (`.online`) — раннер доступен и принимает задачи;
- серая (`.paused`) — раннер на паузе, не берёт задачи;
- красная (`.dead`) — нет heartbeat / offline.

Tooltip на точке показывает `last heartbeat <время>`.

**Статусы пайплайнов:**
- `success` — зелёный;
- `running` — синий с пульсацией;
- `pending` — жёлтый;
- `failed` / `canceled` / `skipped` — красный / серый / затемнённый.

---

## GitLab Runners и Pipelines

Вкладка **GitLab Runners** в UI показывает все раннеры GitLab-инстанса: статус
(online / paused / dead), тип (shared / group / project), теги и время
последнего контакта.

### Авторизация

**Вариант 1 — автоопределение из `glab`** (рекомендуется):

```bash
glab auth login          # один раз, сохраняет токен в ~/.config/glab-cli/config.yml
./kube-ctl ...           # дашборд подхватит GitLab URL и токен автоматически
```

**Вариант 2 — флаги:**

```bash
./kube-ctl -gitlab-url https://gitlab.example.com -gitlab-token glpat-xxxxx
```

**Вариант 3 — переменная окружения:**

```bash
export GITLAB_TOKEN=glpat-xxxxx
./kube-ctl -gitlab-url https://gitlab.example.com
```

Приоритет: флаги > `GITLAB_TOKEN` env > конфиг glab.

Если GitLab не настроен, вкладка остаётся в навигации, но показывает пустое
состояние — остальной дашборд работает как обычно.

### Требования к токену

Для `/api/v4/runners/all` нужен **admin**-токен с scope `read_api`.
Обычный пользовательский токен вернёт только раннеры, к которым у него есть
доступ (`/api/v4/runners`).

### Pipelines

Под таблицей раннеров — панель **GitLab Pipelines**, обновляется каждые 30s.
Пайплайны сгруппированы по проектам, отсортированы по последней активности.

- **Фильтр** — чипсы по именам проектов (выбор сохраняется в localStorage).
- **Карточка** — две строки: `#<id> / <branch>` и мета (`статус · by <user> · длительность · время`).
- **↺ restart / ✕** — кнопки retry и cancel. Бэкенд пробует `glab pipeline retry/cancel`,
  при неудаче — REST API (`POST /api/v4/projects/:id/pipelines/:id/retry`).
- **trends** — кнопка раскрывает тренд за 24h: блочная диаграмма + статистика
  success/failed/other по последним запускам проекта.

```bash
curl -s localhost:8080/api/pipelines | jq
curl -s 'localhost:8080/api/pipelines/trends?project=42' | jq
curl -s -X POST 'localhost:8080/api/pipelines/action' \
  -H 'Content-Type: application/json' \
  -d '{"action":"retry","pipeline_id":123,"project_id":42}'
```

## In-cluster deploy (на потом)

Передай `-kubeconfig=""` чтобы использовать ServiceAccount:

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: kube-ctl
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: kube-ctl-reader
rules:
  - apiGroups: [""]
    resources: [nodes, pods, services, namespaces, events]
    verbs: [get, list]
  - apiGroups: ["apps"]
    resources: [deployments, statefulsets, daemonsets]
    verbs: [get, list]
  - apiGroups: ["metrics.k8s.io"]
    resources: [nodes, pods]
    verbs: [get, list]
```
