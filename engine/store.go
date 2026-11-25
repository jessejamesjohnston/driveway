// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"sort"
	"sync"
)

// memStore is rclone's config held in memory only. The app owns the
// durable copy (encrypted with an Android Keystore key) and fills this at
// startup through SetRemote.
type memStore struct {
	mu   sync.Mutex
	data map[string]map[string]string
	// onChange hears values rclone sets itself (a refreshed token), not the
	// ones the app sets through SetRemote.
	onChange func(section, key, value string)
}

func newMemStore() *memStore { return &memStore{data: map[string]map[string]string{}} }

func (s *memStore) GetSectionList() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.data))
	for k := range s.data {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (s *memStore) HasSection(section string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.data[section]
	return ok
}

func (s *memStore) DeleteSection(section string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, section)
}

func (s *memStore) GetKeyList(section string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.data[section]))
	for k := range s.data[section] {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (s *memStore) GetValue(section, key string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.data[section][key]
	return v, ok
}

func (s *memStore) SetValue(section, key, value string) {
	s.mu.Lock()
	changed := s.data[section][key] != value
	s.set(section, key, value)
	cb := s.onChange
	s.mu.Unlock()
	if changed && cb != nil {
		cb(section, key, value)
	}
}

// setQuiet sets a value without telling onChange (the app's own settings).
func (s *memStore) setQuiet(section, key, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.set(section, key, value)
}

func (s *memStore) set(section, key, value string) {
	if s.data[section] == nil {
		s.data[section] = map[string]string{}
	}
	s.data[section][key] = value
}

func (s *memStore) DeleteKey(section, key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.data[section][key]
	delete(s.data[section], key)
	return ok
}

func (s *memStore) Load() error                { return nil }
func (s *memStore) Save() error                { return nil }
func (s *memStore) Serialize() (string, error) { return "", nil }
