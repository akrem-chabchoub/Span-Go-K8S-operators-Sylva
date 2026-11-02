#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
kubeconfig="$root/bin/span.kubeconfig"
repo="$root/bin/flux-repo"
gitroot="$root/bin/git"
port=8088
kubectl="kubectl --kubeconfig $kubeconfig"

if [ ! -f "$kubeconfig" ]; then
  echo "kind cluster span is not up. Run make kind-demo first."
  exit 1
fi

mkdir -p "$repo" "$gitroot"
rm -rf "$repo/crd" "$repo/rbac" "$repo/helper" "$repo/manager" "$repo/sample" "$repo/webhook" "$repo/kustomization.yaml"
cp "$root/config/crd/bases/span.dev_segments.yaml" "$repo/crd.yaml"
cp "$root/config/rbac/role.yaml" "$repo/rbac.yaml"
cp "$root/config/helper/helper.yaml" "$repo/helper.yaml"
cp "$root/config/manager/manager.yaml" "$repo/manager.yaml"
cp "$root/config/manager/service.yaml" "$repo/service.yaml"
cp "$root/config/samples/span_v1alpha1_segment.yaml" "$repo/sample.yaml"
ca=$(openssl base64 -A -in "$root/bin/certs/tls.crt")
sed "s|U1BBTl9DQV9QTEFDRUhPTERFUg==|$ca|" "$root/config/webhook/manifests.yaml" > "$repo/webhook.yaml"
cat > "$repo/kustomization.yaml" <<'EOF'
resources:
  - crd.yaml
  - rbac.yaml
  - helper.yaml
  - manager.yaml
  - service.yaml
  - webhook.yaml
  - sample.yaml
EOF

if [ ! -d "$repo/.git" ]; then
  git -C "$repo" init -b main
fi
git -C "$repo" add -A
if ! git -C "$repo" diff --cached --quiet; then
  git -C "$repo" -c user.email=span@localhost -c user.name=span commit -m "span lab"
fi
rm -rf "$gitroot/span.git"
git clone --bare "$repo" "$gitroot/span.git"
git -C "$gitroot/span.git" update-server-info

if ! curl -sf "http://127.0.0.1:$port/span.git/info/refs?service=git-upload-pack" >/dev/null; then
  go run "$root/hack/gitsrv" "$gitroot" ":$port" >/tmp/span-gitsrv.log 2>&1 &
  echo $! > "$root/bin/gitsrv.pid"
  i=0
  while ! curl -sf "http://127.0.0.1:$port/span.git/info/refs?service=git-upload-pack" >/dev/null; do
    i=$((i + 1))
    if [ "$i" -gt 50 ]; then
      echo "git server did not start. see /tmp/span-gitsrv.log"
      exit 1
    fi
    sleep 0.2
  done
fi

if ! $kubectl get ns flux-system >/dev/null 2>&1; then
  $kubectl apply -f https://github.com/fluxcd/flux2/releases/download/v2.6.4/install.yaml
  $kubectl -n flux-system rollout status deploy/source-controller --timeout=180s
  $kubectl -n flux-system rollout status deploy/kustomize-controller --timeout=180s
fi
$kubectl apply -f "$root/config/flux/source.yaml"
$kubectl -n flux-system wait gitrepository/span --for=condition=Ready --timeout=120s
$kubectl -n flux-system wait kustomization/span --for=condition=Ready --timeout=180s

$kubectl delete segment lab --ignore-not-found
i=0
while ! $kubectl get segment lab >/dev/null 2>&1; do
  i=$((i + 1))
  if [ "$i" -gt 40 ]; then
    echo "flux did not put the segment back"
    exit 1
  fi
  sleep 3
done
echo "flux put segment/lab back"
