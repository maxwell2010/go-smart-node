#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
[[ "$(protoc --version)" == 'libprotoc 3.21.12' ]]
go version -m "$(command -v protoc-gen-gocosmos)" | grep -Eq 'mod[[:space:]]+github.com/cosmos/gogoproto[[:space:]]+v1.7.2[[:space:]]'
sdk="$(go list -m -f '{{.Dir}}' github.com/cosmos/cosmos-sdk)"
gogo="$(go list -m -f '{{.Dir}}' github.com/cosmos/gogoproto)"
cosmos_proto="$(go list -m -f '{{.Dir}}' github.com/cosmos/cosmos-proto)"
generated="$(mktemp -d /tmp/mintcandy-authority-proto.XXXXXX)"
[[ "$generated" == /tmp/mintcandy-authority-proto.* ]]
trap 'rm -rf -- "$generated"' EXIT
protoc -I proto -I "$sdk/proto" -I "$gogo" -I "$gogo/protobuf" -I "$cosmos_proto/proto" -I /usr/include \
 --gocosmos_out="plugins=grpc,paths=source_relative:$generated" \
 proto/mintcandy/authority/v1/tx.proto
mkdir -p authority/types
cp "$generated"/mintcandy/authority/v1/tx.pb.go authority/types/
