package push

import "sync"

// runPool runs fn(i) for every i in [0, n) using at most concurrency
// goroutines at a time, and waits for all of them to finish. fn is expected
// to write its own result (e.g. into a shared, pre-sized slice indexed by i),
// so callers get results in the original order despite the concurrency.
func runPool(n, concurrency int, fn func(i int)) {
	if n == 0 {
		return
	}
	if concurrency <= 0 || concurrency > n {
		concurrency = n
	}
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := range n {
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			fn(i)
		}(i)
	}
	wg.Wait()
}
