#!/bin/sh
set -eu
echo "span will not boot a Sylva cluster on this machine."
echo "This Mac has no KVM, and 16 GB is not enough for the nested libvirt lab."
echo "Sylva, Metal3, and Cluster API stay upstream. This repo does not copy them."
echo "The kernel proof here is: make helper-self-test"
echo "That programs span0 inside a container network namespace. It is not a workload cluster."
exit 1
