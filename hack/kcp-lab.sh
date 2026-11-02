#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
ver=0.28.3
tgz="$root/bin/kcp_${ver}_darwin_arm64.tar.gz"
bindir="$root/bin"
data="$root/bin/kcp-root"
export KUBECONFIG="$data/admin.kubeconfig"

mkdir -p "$bindir"
if [ ! -x "$bindir/kcp" ]; then
  curl -fsSL -o "$tgz" "https://github.com/kcp-dev/kcp/releases/download/v${ver}/kcp_${ver}_darwin_arm64.tar.gz"
  tar -C "$bindir" -xzf "$tgz"
  if [ -x "$bindir/bin/kcp" ]; then
    mv "$bindir/bin/kcp" "$bindir/kcp"
    mv "$bindir/bin/kubectl-kcp" "$bindir/kubectl-kcp" 2>/dev/null || true
  fi
fi

if ! curl -sf http://127.0.0.1:6443/readyz >/dev/null 2>&1; then
  "$bindir/kcp" start --root-directory "$data" >/tmp/span-kcp.log 2>&1 &
  echo $! > "$root/bin/kcp.pid"
  i=0
  while [ ! -f "$KUBECONFIG" ]; do
    i=$((i + 1))
    if [ "$i" -gt 60 ]; then
      echo "kcp did not write a kubeconfig. see /tmp/span-kcp.log"
      exit 1
    fi
    sleep 1
  done
fi

kubectl apply -f "$root/config/kcp/export.yaml"
rootconfig="$data/admin.kubeconfig"
wskube="$root/bin/kcp-campus.kubeconfig"
cp "$rootconfig" "$wskube"
if [ -x "$bindir/kubectl-kcp" ]; then
  PATH="$bindir:$PATH" KUBECONFIG="$wskube" kubectl kcp workspace create campus || true
  PATH="$bindir:$PATH" KUBECONFIG="$wskube" kubectl kcp workspace use campus
else
  echo "kubectl-kcp was not in the release archive"
  exit 1
fi
KUBECONFIG="$wskube" kubectl apply -f "$root/config/kcp/binding.yaml"
KUBECONFIG="$wskube" kubectl apply -f "$root/config/samples/span_v1alpha1_segment.yaml"
KUBECONFIG="$rootconfig" go run "$root/cmd/kcp" --endpointslice=span >/tmp/span-kcp-controller.log 2>&1 &
echo $! > "$root/bin/kcp-controller.pid"
i=0
while true; do
  reason=$(KUBECONFIG="$wskube" kubectl get segment lab -o jsonpath='{.status.conditions[?(@.type=="Ready")].reason}' 2>/dev/null || true)
  if [ "$reason" = "Planned" ]; then
    echo "kcp workspace has segment/lab Ready=Planned"
    exit 0
  fi
  i=$((i + 1))
  if [ "$i" -gt 40 ]; then
    echo "segment status did not become Planned. see /tmp/span-kcp-controller.log"
    KUBECONFIG="$wskube" kubectl get segment lab -o yaml || true
    exit 1
  fi
  sleep 2
done
