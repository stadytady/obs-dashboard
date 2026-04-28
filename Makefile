.PHONY: tidy run run-multi run-stub build clean

KUBECONFIG ?= $(HOME)/.kube/config
ADDR ?= :8080
STATIC ?= ../frontend
CLUSTERS ?=
SERVERS ?=
GRAFANA_URL ?=
OPENSEARCH_URL ?=
JAEGER_URL ?=

tidy:
	cd backend && go mod tidy

# single-cluster (shortcut флаг -kubeconfig)
run: tidy
	cd backend && go run . \
		-kubeconfig=$(KUBECONFIG) \
		-addr=$(ADDR) \
		-static=$(STATIC) \
		$(if $(GRAFANA_URL),-grafana-url=$(GRAFANA_URL),) \
		$(if $(OPENSEARCH_URL),-opensearch-url=$(OPENSEARCH_URL),) \
		$(if $(JAEGER_URL),-jaeger-url=$(JAEGER_URL),)

# multi-cluster: make run-multi CLUSTERS=./clusters.yaml [SERVERS=./servers.yaml]
run-multi: tidy
	@if [ -z "$(CLUSTERS)" ]; then echo "usage: make run-multi CLUSTERS=path/to/clusters.yaml [SERVERS=path/to/servers.yaml]"; exit 1; fi
	cd backend && go run . \
		-clusters=$(abspath $(CLUSTERS)) \
		$(if $(SERVERS),-servers=$(abspath $(SERVERS)),) \
		-addr=$(ADDR) \
		-static=$(STATIC) \
		$(if $(GRAFANA_URL),-grafana-url=$(GRAFANA_URL),) \
		$(if $(OPENSEARCH_URL),-opensearch-url=$(OPENSEARCH_URL),) \
		$(if $(JAEGER_URL),-jaeger-url=$(JAEGER_URL),)

# локальный запуск без кластера — все данные синтетические
run-stub: tidy
	cd backend && go run . \
		-stub \
		-addr=$(ADDR) \
		-static=$(STATIC)

build: tidy
	cd backend && CGO_ENABLED=0 go build -o ../kube-ctl -ldflags="-s -w" .

clean:
	rm -f kube-ctl
