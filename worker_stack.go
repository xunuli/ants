package ants

import "time"

type workerStack struct {
	// 用于存放所有可用的worker的列表
	items []worker
	// 用于临时存放已过期的worker的集合
	expiry []worker
}

// 新建一个worker栈队列
func newWorkerStack(size int) *workerStack {
	return &workerStack{
		items: make([]worker, 0, size),
	}
}

// 返回worker队列的长度
func (wq *workerStack) len() int {
	return len(wq.items)
}

// 返回该队列是否为空
func (wq *workerStack) isEmpty() bool {
	return len(wq.items) == 0
}

// 将worker加入worker队列中
func (wq *workerStack) insert(w worker) error {
	wq.items = append(wq.items, w)
	return nil
}

// 弹出栈队列中最后一个worker
func (wq *workerStack) detach() worker {
	l := wq.len()
	if l == 0 {
		return nil
	}

	w := wq.items[l-1]
	wq.items[l-1] = nil // avoid memory leaks
	wq.items = wq.items[:l-1]

	return w
}

// 清除那些过期的worker，把它们放入expier队列中，并返回
func (wq *workerStack) refresh(duration time.Duration) []worker {
	n := wq.len()
	if n == 0 {
		return nil
	}

	// 当前时间减去过期时间就是已经过期了的时间
	expiryTime := time.Now().Add(-duration)
	// 因为存入栈的顺序具有先后顺序，先存的肯定先先过期
	// 采用二分查找将已经过期的worker的最后一个索引取出来
	index := wq.binarySearch(0, n-1, expiryTime)

	wq.expiry = wq.expiry[:0]
	if index != -1 {
		wq.expiry = append(wq.expiry, wq.items[:index+1]...)
		m := copy(wq.items, wq.items[index+1:])
		for i := m; i < n; i++ {
			wq.items[i] = nil
		}
		wq.items = wq.items[:m]
	}
	return wq.expiry
}

// 采用二分查找快速寻找达到过期时间的worker
func (wq *workerStack) binarySearch(l, r int, expiryTime time.Time) int {
	for l <= r {
		mid := l + ((r - l) >> 1) // avoid overflow when computing mid
		if expiryTime.Before(wq.items[mid].lastUsedTime()) {
			r = mid - 1
		} else {
			l = mid + 1
		}
	}
	return r
}

// 重置worker队列，清空
func (wq *workerStack) reset() {
	for i := 0; i < wq.len(); i++ {
		wq.items[i].finish()
		wq.items[i] = nil
	}
	wq.items = wq.items[:0]
}
