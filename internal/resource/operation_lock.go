/*
  Copyright © 2026 Alexey Shulutkov <github@shulutkov.ru>

  Licensed under the Apache License, Version 2.0 (the "License");
  you may not use this file except in compliance with the License.
  You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

  Unless required by applicable law or agreed to in writing, software
  distributed under the License is distributed on an "AS IS" BASIS,
  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
  See the License for the specific language governing permissions and
  limitations under the License.
*/

package resource

import (
	"context"
	"sync"
)

type operationLocks struct {
	mu     sync.Mutex
	locked map[string]*operationLock
}

type operationLock struct {
	token chan struct{}
	refs  int
}

func newOperationLocks() *operationLocks {
	return &operationLocks{locked: make(map[string]*operationLock)}
}

func (l *operationLocks) lock(ctx context.Context, key string) (func(), error) {
	l.mu.Lock()
	item := l.locked[key]
	if item == nil {
		item = &operationLock{token: make(chan struct{}, 1)}
		item.token <- struct{}{}
		l.locked[key] = item
	}
	item.refs++
	l.mu.Unlock()
	select {
	case <-item.token:
		return func() {
			item.token <- struct{}{}
			l.mu.Lock()
			item.refs--
			if item.refs == 0 {
				delete(l.locked, key)
			}
			l.mu.Unlock()
		}, nil
	case <-ctx.Done():
		l.mu.Lock()
		item.refs--
		if item.refs == 0 {
			delete(l.locked, key)
		}
		l.mu.Unlock()
		return nil, ctx.Err()
	}
}
