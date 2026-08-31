package tapper

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

var configOwnedFields = map[string]struct{}{
	"logFile": {}, "logLevel": {}, "updated": {}, "defaultKeg": {},
	"fallbackKeg": {}, "flight": {}, "agent": {}, "kegMap": {},
	"namespaces": {}, "defaultHub": {}, "fallbackHub": {},
	"defaultNamespace": {}, "fallbackNamespace": {}, "disableAtlasHub": {},
	"disableTelemetry": {}, "hubs": {}, "agents": {},
}

var configObjectOwnedFields = map[string]map[string]struct{}{
	"hubs": {
		"kind": {}, "defaultNamespace": {}, "url": {}, "token": {}, "tokenEnv": {},
	},
	"namespaces": {"hub": {}},
	"agents": {
		"model": {}, "baseUrl": {}, "auth": {}, "apiKeyEnv": {},
		"contextWindow": {}, "args": {},
	},
}

var kegMapOwnedFields = map[string]struct{}{
	"alias": {}, "pathPrefix": {}, "pathRegex": {},
}

func overlayConfigDocument(original *yaml.Node, data *configDTO) (*yaml.Node, error) {
	typedRaw, err := yaml.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("marshal typed config: %w", err)
	}
	var typed yaml.Node
	if err := yaml.Unmarshal(typedRaw, &typed); err != nil {
		return nil, fmt.Errorf("decode typed config document: %w", err)
	}
	if original == nil || mappingNode(original) == nil {
		return cloneYAMLNode(&typed), nil
	}

	out := cloneYAMLNode(original)
	dst := mappingNode(out)
	src := mappingNode(&typed)
	for field := range configOwnedFields {
		srcValue, present := mappingValue(src, field)
		if !present {
			removeMappingValue(dst, field)
			continue
		}
		dstValue, exists := mappingValue(dst, field)
		switch field {
		case "hubs", "namespaces", "agents":
			if exists && dstValue.Kind == yaml.MappingNode && srcValue.Kind == yaml.MappingNode {
				overlayNamedObjects(dstValue, srcValue, configObjectOwnedFields[field])
			} else {
				setMappingValue(dst, field, cloneYAMLNode(srcValue))
			}
		case "kegMap":
			if exists && dstValue.Kind == yaml.SequenceNode && srcValue.Kind == yaml.SequenceNode {
				overlaySequenceObjects(dstValue, srcValue, kegMapOwnedFields)
			} else {
				setMappingValue(dst, field, cloneYAMLNode(srcValue))
			}
		default:
			setMappingValue(dst, field, cloneYAMLNode(srcValue))
		}
	}
	return out, nil
}

func overlayNamedObjects(dst, src *yaml.Node, owned map[string]struct{}) {
	wanted := make(map[string]*yaml.Node, len(src.Content)/2)
	for i := 0; i+1 < len(src.Content); i += 2 {
		wanted[src.Content[i].Value] = src.Content[i+1]
	}
	for i := len(dst.Content) - 2; i >= 0; i -= 2 {
		if _, ok := wanted[dst.Content[i].Value]; !ok {
			dst.Content = append(dst.Content[:i], dst.Content[i+2:]...)
		}
	}
	for name, srcValue := range wanted {
		dstValue, ok := mappingValue(dst, name)
		if ok && dstValue.Kind == yaml.MappingNode && srcValue.Kind == yaml.MappingNode {
			overlayOwnedMapping(dstValue, srcValue, owned)
			continue
		}
		setMappingValue(dst, name, cloneYAMLNode(srcValue))
	}
}

func overlaySequenceObjects(dst, src *yaml.Node, owned map[string]struct{}) {
	common := len(dst.Content)
	if len(src.Content) < common {
		common = len(src.Content)
	}
	for i := 0; i < common; i++ {
		if dst.Content[i].Kind == yaml.MappingNode && src.Content[i].Kind == yaml.MappingNode {
			overlayOwnedMapping(dst.Content[i], src.Content[i], owned)
		} else {
			dst.Content[i] = cloneYAMLNode(src.Content[i])
		}
	}
	if len(dst.Content) > len(src.Content) {
		dst.Content = dst.Content[:len(src.Content)]
	}
	for i := common; i < len(src.Content); i++ {
		dst.Content = append(dst.Content, cloneYAMLNode(src.Content[i]))
	}
}

func overlayOwnedMapping(dst, src *yaml.Node, owned map[string]struct{}) {
	for field := range owned {
		value, ok := mappingValue(src, field)
		if !ok {
			removeMappingValue(dst, field)
			continue
		}
		setMappingValue(dst, field, cloneYAMLNode(value))
	}
}

func mappingNode(doc *yaml.Node) *yaml.Node {
	if doc == nil {
		return nil
	}
	if doc.Kind == yaml.DocumentNode {
		if len(doc.Content) == 0 {
			return nil
		}
		return doc.Content[0]
	}
	if doc.Kind == yaml.MappingNode {
		return doc
	}
	return nil
}

func mappingValue(mapping *yaml.Node, key string) (*yaml.Node, bool) {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil, false
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1], true
		}
	}
	return nil, false
}

func setMappingValue(mapping *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content[i+1] = value
			return
		}
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)
}

func removeMappingValue(mapping *yaml.Node, key string) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content = append(mapping.Content[:i], mapping.Content[i+2:]...)
			return
		}
	}
}

func cloneYAMLNode(node *yaml.Node) *yaml.Node {
	if node == nil {
		return nil
	}
	clone := *node
	clone.Content = make([]*yaml.Node, len(node.Content))
	for i, child := range node.Content {
		clone.Content[i] = cloneYAMLNode(child)
	}
	return &clone
}
