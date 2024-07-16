// MIT License

// Copyright (c) 2018 Andy Pan

// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package ants

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	syncx "github.com/panjf2000/ants/v2/internal/sync"
)

// poolCommon 是协程池的通用结构体，它包含了协程池的通用结构体
type poolCommon struct {
	// capacity of the pool, a negative value means that the capacity of pool is limitless, an infinite pool is used to
	// avoid potential issue of endless blocking caused by nested usage of a pool: submitting a task to pool
	// which submits a new task to the same pool.
	// capacity是协程池的容量，负值表示容量无限制。
	// 无限制容量的协程池用于避免由于协程池嵌套使用导致无限阻塞问题：向协程池提交一个任务，该任务又向同一个协程池提交一个新任务。
	capacity int32

	// running is the number of the currently running goroutines.
	// running 是当前运行的协程数量
	running int32

	// lock for protecting the worker queue.
	// 锁，用于保护worker队列
	lock sync.Locker

	// workers is a slice that store the available workers.
	// workers 是一个用于存储可用worker的队列（真正意义上的协程池）
	workers workerQueue

	// state is used to notice the pool to closed itself.
	// 协程池状态的标识，0-打开，1-关闭
	state int32

	// cond for waiting to get an idle worker.
	// 并发协调器，用于等待获取空闲worker，（阻塞模式下，挂起和唤醒等待资源的协程）
	cond *sync.Cond

	// done is used to indicate that all workers are done.
	// 用于标识所有workers已经完成
	allDone chan struct{}
	// once is used to make sure the pool is closed just once.
	// once用于确保pool在关闭时只执行一次
	once *sync.Once

	// workerCache speeds up the obtainment of a usable worker in function:retrieveWorker.
	// workerCache 用于加速在retrieveWorker函数中获取可用worker的速度
	workerCache sync.Pool

	// waiting is the number of goroutines already been blocked on pool.Submit(), protected by pool.lock
	// waiting 是阻塞在pool.Submit()上的协程数量，用pool.lock保护
	waiting int32

	// purgeDone 用于指示清楚操作是否已经完成
	purgeDone int32
	// purgeCtx 用于取消清除操作
	purgeCtx context.Context
	// stopPurge 用于停止清除操作
	stopPurge context.CancelFunc

	// 用于指示定时器操作是否已经完成
	ticktockDone int32
	// 用于取消定时器操作
	ticktockCtx context.Context
	// 用于停止定时器操作
	stopTicktock context.CancelFunc

	// now 用于记录当前时间，用于性能优化
	now atomic.Value

	// options 是协程池的配置选项
	options *Options
}

// Pool accepts the tasks and process them concurrently,
// it limits the total of goroutines to a given number by recycling goroutines.
// pool是一个协程池，它接收任务并并发地处理它们，通过复用协程来限制协程数量
type Pool struct {
	poolCommon
}

// purgeStaleWorkers clears stale workers periodically, it runs in an individual goroutine, as a scavenger.
// 定期清除过期的worker
func (p *Pool) purgeStaleWorkers() {
	// 定时器
	ticker := time.NewTicker(p.options.ExpiryDuration)

	// 退出时执行，定时器关闭，purgedone为1，表示清除工作完成
	defer func() {
		ticker.Stop()
		atomic.StoreInt32(&p.purgeDone, 1)
	}()

	purgeCtx := p.purgeCtx // copy to the local variable to avoid race from Reboot()
	for {
		select {
		// 发送取消信号
		case <-purgeCtx.Done():
			return
		case <-ticker.C:
		}

		if p.IsClosed() {
			break
		}

		var isDormant bool
		p.lock.Lock()
		// 调用refersh方法，清除
		staleWorkers := p.workers.refresh(p.options.ExpiryDuration)
		n := p.Running()
		isDormant = n == 0 || n == len(staleWorkers)
		p.lock.Unlock()

		// Notify obsolete workers to stop.
		// This notification must be outside the p.lock, since w.task
		// may be blocking and may consume a lot of time if many workers
		// are located on non-local CPUs.
		// 循环遍历清除队列的worker，停止他们，并设置为nil
		for i := range staleWorkers {
			staleWorkers[i].finish()
			staleWorkers[i] = nil
		}

		// There might be a situation where all workers have been cleaned up (no worker is running),
		// while some invokers still are stuck in p.cond.Wait(), then we need to awake those invokers.
		// 唤醒等待的worker
		if isDormant && p.Waiting() > 0 {
			p.cond.Broadcast()
		}
	}
}

// ticktock is a goroutine that updates the current time in the pool regularly.
// ticktock是一个goroutine，用于更新协程池中的当前时间
func (p *Pool) ticktock() {
	ticker := time.NewTicker(nowTimeUpdateInterval)
	defer func() {
		ticker.Stop()
		atomic.StoreInt32(&p.ticktockDone, 1)
	}()

	ticktockCtx := p.ticktockCtx // copy to the local variable to avoid race from Reboot()
	for {
		select {
		case <-ticktockCtx.Done():
			return
		case <-ticker.C:
		}

		if p.IsClosed() {
			break
		}

		p.now.Store(time.Now())
	}
}

// 执行清除操作
func (p *Pool) goPurge() {
	if p.options.DisablePurge {
		return
	}

	// Start a goroutine to clean up expired workers periodically.
	p.purgeCtx, p.stopPurge = context.WithCancel(context.Background())
	go p.purgeStaleWorkers()
}

func (p *Pool) goTicktock() {
	p.now.Store(time.Now())
	p.ticktockCtx, p.stopTicktock = context.WithCancel(context.Background())
	go p.ticktock()
}

func (p *Pool) nowTime() time.Time {
	return p.now.Load().(time.Time)
}

// NewPool instantiates a Pool with customized options.
// 初始化一个协程池
func NewPool(size int, options ...Option) (*Pool, error) {
	if size <= 0 {
		size = -1
	}
	// 读取用户的配置，做一些前置校验，默认值赋值等前置处理
	opts := loadOptions(options...)

	// 如果开启定期清除worker的操作，设置过期时间
	if !opts.DisablePurge {
		if expiry := opts.ExpiryDuration; expiry < 0 {
			return nil, ErrInvalidPoolExpiry
		} else if expiry == 0 {
			opts.ExpiryDuration = DefaultCleanIntervalTime
		}
	}

	// 设置默认的log
	if opts.Logger == nil {
		opts.Logger = defaultLogger
	}

	// 初始化pool
	p := &Pool{poolCommon: poolCommon{
		capacity: int32(size),
		allDone:  make(chan struct{}),
		lock:     syncx.NewSpinLock(),
		once:     &sync.Once{},
		options:  opts,
	}}

	// 初始化对象池，用于缓存被释放的worker，用于复用
	p.workerCache.New = func() interface{} {
		return &goWorker{
			pool: p,
			task: make(chan func(), workerChanCap),
		}
	}
	// 预先分配内存
	if p.options.PreAlloc {
		if size == -1 {
			return nil, ErrInvalidPreAllocSize
		}
		p.workers = newWorkerQueue(queueTypeLoopQueue, size)
	} else {
		p.workers = newWorkerQueue(queueTypeStack, 0)
	}

	// 新建一个并发控制器件
	p.cond = sync.NewCond(p.lock)

	// 启动清除操作
	p.goPurge()
	// 开启定时器
	p.goTicktock()

	return p, nil
}

// Submit submits a task to this pool.
//
// Note that you are allowed to call Pool.Submit() from the current Pool.Submit(),
// but what calls for special attention is that you will get blocked with the last
// Pool.Submit() call once the current Pool runs out of its capacity, and to avoid this,
// you should instantiate a Pool with ants.WithNonblocking(true).
// 提交一个任务到协程池中
func (p *Pool) Submit(task func()) error {
	if p.IsClosed() {
		return ErrPoolClosed
	}

	w, err := p.retrieveWorker()
	if w != nil {
		w.inputFunc(task)
	}
	return err
}

// Running returns the number of workers currently running.
// 获取正在运行的worker数量
func (p *Pool) Running() int {
	return int(atomic.LoadInt32(&p.running))
}

// Free returns the number of available workers, -1 indicates this pool is unlimited.
// Free表示目前空闲的worker数量
func (p *Pool) Free() int {
	c := p.Cap()
	if c < 0 {
		return -1
	}
	return c - p.Running()
}

// Waiting returns the number of tasks waiting to be executed.
// 阻塞等待的任务数量
func (p *Pool) Waiting() int {
	return int(atomic.LoadInt32(&p.waiting))
}

// Cap returns the capacity of this pool.
// 容量
func (p *Pool) Cap() int {
	return int(atomic.LoadInt32(&p.capacity))
}

// Tune changes the capacity of this pool, note that it is noneffective to the infinite or pre-allocation pool.
// 动态调整协程池的容量
func (p *Pool) Tune(size int) {
	capacity := p.Cap()
	if capacity == -1 || size <= 0 || size == capacity || p.options.PreAlloc {
		return
	}
	atomic.StoreInt32(&p.capacity, int32(size))
	if size > capacity {
		if size-capacity == 1 {
			p.cond.Signal()
			return
		}
		p.cond.Broadcast()
	}
}

// IsClosed indicates whether the pool is closed.
func (p *Pool) IsClosed() bool {
	return atomic.LoadInt32(&p.state) == CLOSED
}

// Release closes this pool and releases the worker queue.
// 关闭协程池并释放所有资源
func (p *Pool) Release() {
	if !atomic.CompareAndSwapInt32(&p.state, OPENED, CLOSED) {
		return
	}

	if p.stopPurge != nil {
		p.stopPurge()
		p.stopPurge = nil
	}
	if p.stopTicktock != nil {
		p.stopTicktock()
		p.stopTicktock = nil
	}

	p.lock.Lock()
	p.workers.reset()
	p.lock.Unlock()
	// There might be some callers waiting in retrieveWorker(), so we need to wake them up to prevent
	// those callers blocking infinitely.
	p.cond.Broadcast()
}

// ReleaseTimeout is like Release but with a timeout, it waits all workers to exit before timing out.
func (p *Pool) ReleaseTimeout(timeout time.Duration) error {
	if p.IsClosed() || (!p.options.DisablePurge && p.stopPurge == nil) || p.stopTicktock == nil {
		return ErrPoolClosed
	}

	p.Release()

	var purgeCh <-chan struct{}
	if !p.options.DisablePurge {
		purgeCh = p.purgeCtx.Done()
	} else {
		purgeCh = p.allDone
	}

	if p.Running() == 0 {
		p.once.Do(func() {
			close(p.allDone)
		})
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
			return ErrTimeout
		case <-p.allDone:
			<-purgeCh
			<-p.ticktockCtx.Done()
			if p.Running() == 0 &&
				(p.options.DisablePurge || atomic.LoadInt32(&p.purgeDone) == 1) &&
				atomic.LoadInt32(&p.ticktockDone) == 1 {
				return nil
			}
		}
	}
}

// Reboot reboots a closed pool, it does nothing if the pool is not closed.
// If you intend to reboot a closed pool, use ReleaseTimeout() instead of
// Release() to ensure that all workers are stopped and resource are released
// before rebooting, otherwise you may run into data race.
func (p *Pool) Reboot() {
	if atomic.CompareAndSwapInt32(&p.state, CLOSED, OPENED) {
		atomic.StoreInt32(&p.purgeDone, 0)
		p.goPurge()
		atomic.StoreInt32(&p.ticktockDone, 0)
		p.goTicktock()
		p.allDone = make(chan struct{})
		p.once = &sync.Once{}
	}
}

func (p *Pool) addRunning(delta int) int {
	return int(atomic.AddInt32(&p.running, int32(delta)))
}

func (p *Pool) addWaiting(delta int) {
	atomic.AddInt32(&p.waiting, int32(delta))
}

// retrieveWorker returns an available worker to run the tasks.
// retrieveWorker 返回一个可用的worker运行任务
func (p *Pool) retrieveWorker() (w worker, err error) {
	// 访问可以队列时需要加锁
	p.lock.Lock()

retry:
	// 第一次尝试获取worker
	// First try to fetch the worker from the queue.
	if w = p.workers.detach(); w != nil {
		p.lock.Unlock()
		return
	}

	// If the worker queue is empty, and we don't run out of the pool capacity,
	// then just spawn a new worker goroutine.
	// 如果worker队列为空，如果未达到池子的容量，就启动一个worker运行任务
	if capacity := p.Cap(); capacity == -1 || capacity > p.Running() {
		p.lock.Unlock()
		w = p.workerCache.Get().(*goWorker)
		w.run()
		return
	}

	// Bail out early if it's in nonblocking mode or the number of pending callers reaches the maximum limit value.
	if p.options.Nonblocking || (p.options.MaxBlockingTasks != 0 && p.Waiting() >= p.options.MaxBlockingTasks) {
		p.lock.Unlock()
		return nil, ErrPoolOverload
	}

	// Otherwise, we'll have to keep them blocked and wait for at least one worker to be put back into pool.
	p.addWaiting(1)
	p.cond.Wait() // block and wait for an available worker
	p.addWaiting(-1)

	if p.IsClosed() {
		p.lock.Unlock()
		return nil, ErrPoolClosed
	}

	goto retry
}

// revertWorker puts a worker back into free pool, recycling the goroutines.
// 将goworker放回空闲队列，循环利用goworker
func (p *Pool) revertWorker(worker *goWorker) bool {
	// 检查运行的数量是否超过容量，或者协程池是否关闭，满足则返回false
	if capacity := p.Cap(); (capacity > 0 && p.Running() > capacity) || p.IsClosed() {
		p.cond.Broadcast()
		return false
	}

	// 更新最后时间为当前时间
	worker.lastUsed = p.nowTime()

	p.lock.Lock()
	// To avoid memory leaks, add a double check in the lock scope.
	// Issue: https://github.com/panjf2000/ants/issues/113
	if p.IsClosed() {
		p.lock.Unlock()
		return false
	}
	// 将worker放回worker队列中
	if err := p.workers.insert(worker); err != nil {
		p.lock.Unlock()
		return false
	}
	// Notify the invoker stuck in 'retrieveWorker()' of there is an available worker in the worker queue.
	p.cond.Signal()
	p.lock.Unlock()

	return true
}
