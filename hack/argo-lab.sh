#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
fluxkube="$root/bin/span.kubeconfig"
argokube="$root/bin/span-argo.kubeconfig"
kubectlargo="kubectl --kubeconfig $argokube"
kubectlspan="kubectl --kubeconfig $fluxkube"

if [ ! -f "$fluxkube" ]; then
  echo "run make kind-demo and hack/flux-lab.sh first"
  exit 1
fi
if ! kind get clusters | grep -qx span-argo; then
  kind create cluster --name span-argo --kubeconfig "$argokube"
else
  kind export kubeconfig --name span-argo --kubeconfig "$argokube"
fi

if ! $kubectlargo get ns argocd >/dev/null 2>&1; then
  $kubectlargo create namespace argocd
  $kubectlargo apply -n argocd -f https://raw.githubusercontent.com/argoproj/argo-cd/v2.14.11/manifests/install.yaml
  $kubectlargo -n argocd rollout status deploy/argocd-server --timeout=300s
fi
$kubectlargo apply -f "$root/config/argocd/application.yaml"
$kubectlargo -n argocd wait application/span --for=jsonpath='{.status.sync.status}'=Synced --timeout=180s

if $kubectlspan get ns argocd >/dev/null 2>&1; then
  echo "argocd is on the flux cluster. that is the thing this lab must not do."
  exit 1
fi
echo "argo synced span-argo. the flux cluster has no argocd namespace."
