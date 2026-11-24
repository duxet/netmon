package config

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"go.yaml.in/yaml/v3"
)

// editYAML returns the YAML document data with the settings in set changed
// or added, and those in unset removed.
//
// It edits the text, so that comments, blank lines and the way values are
// written stay as they were, as people keep this file by hand too. Only when
// a setting is written in a way it can't change in place, such as over
// several lines, does it write the whole document anew, which keeps the
// comments but not the blank lines.
func editYAML(data []byte, set map[string]any, unset []string) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}

	var root *yaml.Node
	if len(doc.Content) > 0 {
		root = doc.Content[0]
		if root.Kind != yaml.MappingNode && !isNull(root) {
			return nil, errors.New("the file must be a list of settings, one per line, such as local_dns: 192.168.1.1")
		}
	}

	want, err := expected(data, set, unset)
	if err != nil {
		return nil, err
	}

	if out, ok := editText(data, root, set, unset); ok && holds(out, want) {
		return out, nil
	}

	out, err := rewrite(&doc, root, set, unset)
	if err != nil {
		return nil, err
	}
	if !holds(out, want) {
		return nil, errors.New("the settings could not be written into the file")
	}
	return out, nil
}

func isNull(n *yaml.Node) bool {
	return n.Kind == yaml.ScalarNode && n.Tag == "!!null"
}

// keysIn returns the keys of set and unset in the order of the settings, so
// that new settings are added in the order Config lists them.
func keysIn(set map[string]any, unset []string) (setKeys, unsetKeys []string) {
	for _, key := range settings() {
		if _, ok := set[key]; ok {
			setKeys = append(setKeys, key)
		}
		for _, u := range unset {
			if u == key {
				unsetKeys = append(unsetKeys, key)
			}
		}
	}
	return setKeys, unsetKeys
}

// scalar writes a value as YAML, quoted where it has to be.
func scalar(value any) (string, error) {
	out, err := yaml.Marshal(map[string]any{"k": value})
	if err != nil {
		return "", err
	}
	text := strings.TrimSuffix(strings.TrimPrefix(string(out), "k: "), "\n")
	if strings.Contains(text, "\n") {
		return "", fmt.Errorf("%v can't be written on one line", value)
	}
	return text, nil
}

// editText changes the lines of the settings in data. It reports false when
// one of them isn't a value on the line of its key, which it can't change in
// place.
func editText(data []byte, root *yaml.Node, set map[string]any, unset []string) ([]byte, bool) {
	if root != nil && root.Kind == yaml.MappingNode && root.Style&yaml.FlowStyle != 0 {
		return nil, false
	}

	lines := strings.SplitAfter(string(data), "\n")
	type entry struct {
		key, value *yaml.Node
		// last is the index of the line after which the next key starts.
		last int
	}
	entries := map[string]entry{}
	if root != nil && root.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(root.Content); i += 2 {
			last := len(lines) - 1
			if i+2 < len(root.Content) {
				last = root.Content[i+2].Line - 2
			}
			entries[root.Content[i].Value] = entry{key: root.Content[i], value: root.Content[i+1], last: last}
		}
	}

	// inline reports whether a value is on its key's line alone, with only
	// comments and blank lines up to the next key.
	inline := func(e entry) bool {
		v := e.value
		if v.Kind != yaml.ScalarNode || v.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0 ||
			v.Line != e.key.Line || e.key.Column != 1 || v.Anchor != "" || (isNull(v) && v.Value == "") {
			return false
		}
		for i := e.key.Line; i <= e.last && i < len(lines); i++ {
			text := strings.TrimSpace(lines[i])
			if text != "" && !strings.HasPrefix(text, "#") {
				return false
			}
		}
		return true
	}

	setKeys, unsetKeys := keysIn(set, unset)
	drop := map[int]bool{}
	var added []string
	for _, key := range setKeys {
		value, err := scalar(set[key])
		if err != nil {
			return nil, false
		}
		e, ok := entries[key]
		if !ok {
			added = append(added, key+": "+value)
			continue
		}
		if !inline(e) {
			return nil, false
		}

		line := lines[e.key.Line-1]
		ending := line[len(strings.TrimRight(line, "\r\n")):]
		runes := []rune(strings.TrimRight(line, "\r\n"))
		if e.value.Column-1 > len(runes) {
			return nil, false
		}
		text := string(runes[:e.value.Column-1]) + value
		if e.value.LineComment != "" {
			text += " " + e.value.LineComment
		}
		lines[e.key.Line-1] = text + ending
	}
	for _, key := range unsetKeys {
		if e, ok := entries[key]; ok {
			if !inline(e) {
				return nil, false
			}
			drop[e.key.Line-1] = true
		}
	}

	var b strings.Builder
	for i, line := range lines {
		if !drop[i] {
			b.WriteString(line)
		}
	}
	if len(added) > 0 {
		text := b.String()
		if text != "" && !strings.HasSuffix(text, "\n") {
			b.WriteString("\n")
		}
		for _, line := range added {
			b.WriteString(line + "\n")
		}
	}
	return []byte(b.String()), true
}

// rewrite changes the settings in the parsed document and writes all of it.
func rewrite(doc *yaml.Node, root *yaml.Node, set map[string]any, unset []string) ([]byte, error) {
	if root == nil || root.Kind != yaml.MappingNode {
		root = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		doc.Kind = yaml.DocumentNode
		doc.Content = []*yaml.Node{root}
	}
	root.Style &^= yaml.FlowStyle

	setKeys, unsetKeys := keysIn(set, unset)
	for _, key := range setKeys {
		var value yaml.Node
		if err := value.Encode(set[key]); err != nil {
			return nil, err
		}
		found := false
		for i := 0; i+1 < len(root.Content); i += 2 {
			if root.Content[i].Value == key {
				old := root.Content[i+1]
				value.HeadComment, value.LineComment, value.FootComment = old.HeadComment, old.LineComment, old.FootComment
				root.Content[i+1] = &value
				found = true
			}
		}
		if !found {
			root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, &value)
		}
	}
	for _, key := range unsetKeys {
		for i := 0; i+1 < len(root.Content); i += 2 {
			if root.Content[i].Value == key {
				root.Content = append(root.Content[:i], root.Content[i+2:]...)
				break
			}
		}
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// expected returns the settings the file should hold after the change.
func expected(data []byte, set map[string]any, unset []string) (map[string]any, error) {
	values := map[string]any{}
	if err := yaml.Unmarshal(data, &values); err != nil {
		return nil, err
	}
	// A document that is only null leaves no map.
	if values == nil {
		values = map[string]any{}
	}
	for _, key := range unset {
		delete(values, key)
	}
	for key, value := range set {
		// Through YAML and back, so that numbers compare as YAML reads them.
		text, err := scalar(value)
		if err != nil {
			return nil, err
		}
		var parsed any
		if err := yaml.Unmarshal([]byte(text), &parsed); err != nil {
			return nil, err
		}
		values[key] = parsed
	}
	return values, nil
}

// holds reports whether the YAML document data has exactly the settings in want.
func holds(data []byte, want map[string]any) bool {
	got := map[string]any{}
	if err := yaml.Unmarshal(data, &got); err != nil {
		return false
	}
	if got == nil {
		got = map[string]any{}
	}
	return reflect.DeepEqual(got, want)
}
