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
	"runtime/debug"
	"time"
)

// goWorker is the actual executor who runs the tasks,
// it starts a goroutine that accepts tasks and
// performs function calls.
// goWorker是实际执行任务的工作者，它启动一个协程，接收任务并执行函数调用，是worker接口的实现
type goWorker struct {
	// pool who owns this worker.
	// 哪一个协程池拥有该worker
	pool *Pool

	// task is a job should be done.
	// 任务队列，存放需要执行的任务
	task chan func()

	// lastUsed will be updated when putting a worker back into queue.
	// 上一次使用的时间，用于管理协程池中的空闲的工作者（回收空闲的worker），当上一次工作完成之后放回空闲队列时会更新改字段
	lastUsed time.Time
}

// run starts a goroutine to repeat the process
// that performs the function calls.
func (w *goWorker) run() {
	// worker运行数量加1
	w.pool.addRunning(1)
	go func() {
		// 当运行完成时执行
		defer func() {
			// 减少运行worker数量，判断线程池是否关闭以及全部协程执行完
			if w.pool.addRunning(-1) == 0 && w.pool.IsClosed() {
				w.pool.once.Do(func() {
					// 关闭allDone，表示所有工作已经完成
					close(w.pool.allDone)
				})
			}
			// 将自己放回对象缓存池中，以便下一次加速获取
			w.pool.workerCache.Put(w)
			//捕获panic
			if p := recover(); p != nil {
				if ph := w.pool.options.PanicHandler; ph != nil {
					ph(p)
				} else {
					w.pool.options.Logger.Printf("worker exits from panic: %v\n%s\n", p, debug.Stack())
				}
			}
			// Call Signal() here in case there are goroutines waiting for available workers.
			// 调用signal，唤醒正在阻塞的协程
			w.pool.cond.Signal()
		}()

		// worker的工作就是不断去任务队列中获取任务并执行
		for f := range w.task {
			if f == nil {
				return
			}
			// 执行任务
			f()
			// 放回工作队列，循环利用
			if ok := w.pool.revertWorker(w); !ok {
				return
			}
		}
	}()
}

// 表示该worker已经完成任务执行，不在向队列加任务
func (w *goWorker) finish() {
	w.task <- nil
}

// 最后一次的使用时间
func (w *goWorker) lastUsedTime() time.Time {
	return w.lastUsed
}

// 将任务加入worker的任务队列
func (w *goWorker) inputFunc(fn func()) {
	w.task <- fn
}

func (w *goWorker) inputParam(interface{}) {
	panic("unreachable")
}
