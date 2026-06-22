# Show recipes when run with no args
default:
    @just --list

binary := "aerospike-ttl-exporter-linux-amd64"
config := "testconf.yaml"
ssh_user := env_var_or_default("SSH_USER", env_var("USER"))
remote_dir := "/tmp"

# Cross-compile, scp, and run on remote host (set AS_USER/AS_PASS to override creds)
deploy hostname:
    #!/usr/bin/env bash
    set -euo pipefail
    echo "==> Building {{binary}}..."
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o {{binary}} .
    deploy_config="{{config}}"
    if [[ -n "${AS_USER:-}" || -n "${AS_PASS:-}" ]]; then
        deploy_config="/tmp/.deploy-conf-$$.yaml"
        cp {{config}} "$deploy_config"
        [[ -n "${AS_USER:-}" ]] && sed -i'' -e "s/^ *username:.*/  username: $AS_USER/" "$deploy_config"
        [[ -n "${AS_PASS:-}" ]] && sed -i'' -e "s/^ *password:.*/  password: $AS_PASS/" "$deploy_config"
        trap 'rm -f "$deploy_config"' EXIT
    fi
    echo "==> Deploying to {{ssh_user}}@{{hostname}}:{{remote_dir}}/"
    ssh {{ssh_user}}@{{hostname}} "rm -rf {{remote_dir}}/{{config}}"
    scp {{binary}} {{ssh_user}}@{{hostname}}:{{remote_dir}}/
    scp "$deploy_config" {{ssh_user}}@{{hostname}}:{{remote_dir}}/{{config}}
    echo "==> Running on {{hostname}}..."
    ssh {{ssh_user}}@{{hostname}} '{{remote_dir}}/{{binary}} -configFile {{remote_dir}}/{{config}}'

# Unit tests (bucket resolvers, gauges, discovery, config decode)
test:
    go test ./...

# Run locally against a remote Aerospike node (set AS_USER/AS_PASS to override creds)
run-remote remotenode:
    #!/usr/bin/env bash
    set -euo pipefail
    run_config="/tmp/testconf-local.yaml"
    sed 's/aerospikeAddr:.*/aerospikeAddr: {{remotenode}}/' {{config}} > "$run_config"
    if [[ -n "${AS_USER:-}" || -n "${AS_PASS:-}" ]]; then
        [[ -n "${AS_USER:-}" ]] && sed -i'' -e "s/^ *username:.*/  username: $AS_USER/" "$run_config"
        [[ -n "${AS_PASS:-}" ]] && sed -i'' -e "s/^ *password:.*/  password: $AS_PASS/" "$run_config"
    fi
    echo "==> Running against {{remotenode}} (config: $run_config)"
    go run . -configFile "$run_config"

# Build only (no deploy)
build:
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o {{binary}} .

# ULTIMATE SAFETY TEST: hermetic docker proof the exporter never writes (gen stable), with a negative control proving the test can detect a write
gen-check:
    ./scripts/gen-stability-test.sh
