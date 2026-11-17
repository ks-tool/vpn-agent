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
	"errors"
	"testing"
	"time"
)

func TestOperationLockWaitHonorsContext(t *testing.T) {
	locks := newOperationLocks()
	unlock, err := locks.lock(context.Background(), "resource")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := locks.lock(ctx, "resource"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lock wait error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("lock wait ignored context: %v", elapsed)
	}
}
