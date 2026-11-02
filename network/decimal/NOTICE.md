# Source provenance

The Decimal coin/NFT schemas, handwritten types, collection utilities and
financial formulas are adapted from this repository's legacy Decimal source
at upstream commit `a46cb48d7965c1a64a495cebb22e735dfe4b9da3`.
The upstream permission notice is retained in `LICENSE`. The bigfloat package
has its own retained license. The owned modern application and third-party
protocol sources retain their separate license notices.

The modern port changes Go import paths, SDK math/address/signing integration,
parameter storage, fresh-network defaults and validation. Generated coin/NFT
code comes from the checked-in `network/proto/decimal` schemas and the pinned
tools in `network/scripts/generate-native-proto.sh`. Go module paths change;
the `decimal.coin.v1` and `decimal.nft.v1` protobuf wire namespaces remain.
Google API protocol dependencies retain their Apache license and headers.

These files stage the separate fresh-network port. They do not replace the
legacy root packages or establish complete Decimal financial compatibility.
