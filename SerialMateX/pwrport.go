package main

import (
	"fmt"
	"io"

	"go.bug.st/serial"
)

// 电源串口：任务运行期间打开设备自己的 COM 口，只写不读。
// SERIAL 口留给被测板卡，两边互不干扰；电源口在任务结束时一律关闭。
//
// 真正的串口实现（go.bug.st/serial）只出现在 openPwrDevice 里，
// 测试通过下面两个 hook 注入假串口，断言写入内容与关闭时机。

// pwrOpenHook 只给测试用：替换打开电源口的实现。
var pwrOpenHook func(PwrDevice) (io.WriteCloser, error)

// pwrWriteHook 只给测试用：替换往电源口写字节的实现。
var pwrWriteHook func([]byte) error

// openPwrDevice 打开设备的电源串口；重复调用会先关掉上一个句柄。
func (a *App) openPwrDevice(dev PwrDevice) error {
	a.closePwrDevice()

	var port io.WriteCloser
	if pwrOpenHook != nil {
		p, err := pwrOpenHook(dev)
		if err != nil {
			return err
		}
		port = p
	} else {
		p, err := serial.Open(dev.Port, buildSerialMode(dev.Baud, dev.DataBits, dev.StopBits, dev.Parity))
		if err != nil {
			return err
		}
		p.SetDTR(true)
		p.SetRTS(true)
		port = p
	}

	a.pwrMu.Lock()
	a.pwrPort = port
	a.pwrMu.Unlock()
	return nil
}

// closePwrDevice 关闭电源口；重复调用安全（defer 与错误路径都会走到）。
func (a *App) closePwrDevice() {
	a.pwrMu.Lock()
	port := a.pwrPort
	a.pwrPort = nil
	a.pwrMu.Unlock()

	if port != nil {
		_ = port.Close()
	}
}

// writePwrPort 往电源口写一段字节；未打开时返回错误，绝不 panic。
func (a *App) writePwrPort(b []byte) error {
	var err error
	if pwrWriteHook != nil {
		err = pwrWriteHook(b)
	} else {
		a.pwrMu.Lock()
		port := a.pwrPort
		a.pwrMu.Unlock()
		if port == nil {
			return fmt.Errorf("电源口未打开")
		}
		_, err = port.Write(b)
	}
	if err != nil {
		return fmt.Errorf("电源口写入失败：%v", err)
	}
	return nil
}
