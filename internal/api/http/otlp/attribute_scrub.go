package otlp

import "google.golang.org/protobuf/reflect/protoreflect"

// stripClientKeyAttributes removes the reserved key throughout a decoded signal
// before flattening. Walking the protobuf tree covers scope attributes, events,
// links, every metric data-point type and exemplar filtered attributes, including
// metric types we currently reject. Only stampKey may add the trusted resource ID.
func stripClientKeyAttributes(message protoreflect.Message) {
	if !message.IsValid() {
		return
	}
	message.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		if field.IsMap() {
			return true
		} // OTLP attribute collections use KeyValue lists.
		if field.Message() == nil {
			return true
		}
		if !field.IsList() {
			stripClientKeyAttributes(value.Message())
			return true
		}
		list := value.List()
		kept := 0
		for i := 0; i < list.Len(); i++ {
			item := list.Get(i)
			child := item.Message()
			if field.Message().FullName() == "opentelemetry.proto.common.v1.KeyValue" && child.Get(child.Descriptor().Fields().ByName("key")).String() == "everstack.api_key_id" {
				continue
			}
			stripClientKeyAttributes(child)
			list.Set(kept, item)
			kept++
		}
		list.Truncate(kept)
		return true
	})
}
