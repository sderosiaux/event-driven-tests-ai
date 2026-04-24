package schemaregistry

import "fmt"

// protoHandler is a placeholder for M3-T3.
//
// Confluent SR stores Protobuf schemas as the .proto text body. Decoding
// dynamic messages without compile-time stubs requires either:
//   - a parser that builds google.protobuf descriptorpb.FileDescriptorProto from
//     the .proto text (e.g. bufbuild/protocompile), or
//   - the user uploading a pre-built descriptor set and referencing it by name.
//
// We surface a clear error today and ship the real implementation in M3-T3.
type protoHandler struct{}

func newProtoHandler(_ string) (formatHandler, error) {
	return nil, fmt.Errorf("schemaregistry: PROTOBUF codec not yet implemented (planned M3-T3)")
}

func (h *protoHandler) encode(any) ([]byte, error) {
	return nil, fmt.Errorf("schemaregistry: proto encode not implemented")
}
func (h *protoHandler) decode([]byte) (any, error) {
	return nil, fmt.Errorf("schemaregistry: proto decode not implemented")
}
