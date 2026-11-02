#!/usr/bin/env bash
set -euo pipefail
module_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$module_root"
[[ "$(protoc --version)" == 'libprotoc 3.21.12' ]] || { echo 'protoc 3.21.12 required' >&2; exit 1; }
go version -m "$(command -v protoc-gen-gocosmos)" | grep -Eq 'mod[[:space:]]+github.com/cosmos/gogoproto[[:space:]]+v1.7.2[[:space:]]'
go version -m "$(command -v protoc-gen-grpc-gateway)" | grep -Eq 'mod[[:space:]]+github.com/grpc-ecosystem/grpc-gateway[[:space:]]+v1.16.0[[:space:]]'
sdk="$(go list -m -f '{{.Dir}}' github.com/cosmos/cosmos-sdk)"
gogo="$(go list -m -f '{{.Dir}}' github.com/cosmos/gogoproto)"
comet="$(go list -m -f '{{.Dir}}' github.com/cometbft/cometbft)"
cosmos_proto="$(go list -m -f '{{.Dir}}' github.com/cosmos/cosmos-proto)"
generated="$(mktemp -d /tmp/mintcandy-native-proto.XXXXXX)"
[[ "$generated" == /tmp/mintcandy-native-proto.* ]]
trap 'rm -rf -- "$generated"' EXIT
modules=("$@")
if [[ ${#modules[@]} -eq 0 ]]; then modules=(coin nft validator); fi
for module in "${modules[@]}"; do
  case "$module" in coin|nft|validator) ;; *) echo "unknown native proto module" >&2; exit 1 ;; esac
  protoc \
    -I proto -I "$sdk/proto" -I "$gogo" -I "$gogo/protobuf" \
    -I "$comet/proto" -I "$cosmos_proto/proto" -I proto/third_party -I /usr/include \
    --gocosmos_out="plugins=grpc,paths=source_relative,Mgoogle/protobuf/any.proto=github.com/cosmos/cosmos-sdk/codec/types:$generated" \
    --grpc-gateway_out="logtostderr=true,paths=source_relative:$generated" \
    proto/decimal/"$module"/v1/*.proto
  cp "$generated"/decimal/"$module"/v1/*.go "decimal/$module/types/"
done
