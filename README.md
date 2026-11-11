# AutoDebugSystem

一套用于**嵌入式板卡自动调试 / 老化测试**的工具集，由两个互相配合的部分组成：

| 目录 | 内容 | 运行位置 |
| --- | --- | --- |
| `USBto2xUARTtool/` | **下位机固件**：`F103-Bootloader`（串口升级引导）+ `Kernel`（三路虚拟串口 + 命令行） | STM32F103C8/CB |
| `SerialMateX/` | **上位机软件**：串口 / 网络调试、AUTO CMD 命令序列、TASK 自动轮次测试 | Windows / macOS / Linux 桌面 |

两者配合使用：上位机通过 USB 虚拟串口连下位机，用 `AUTO CMD` 里的 TX/RX 命令驱动被测设备，用 `TASK` 页按“上电 / 运行 / 掉电 / 间隔”循环跑 N 轮，并记录调试日志与功耗数据。

---

## 一、下位机固件 `USBto2xUARTtool`

面向 STM32F103（GCC + Makefile 工程，CubeMX 生成骨架），分两个可独立烧录的固件：

### 1.1 F103-Bootloader —— 串口升级引导

* 上电后 **3 秒倒计时**，期间按任意键进入命令行，否则跳转到 APP；
* 命令行（UART1，115200 8N1）基于 letter-shell：

  | 命令 | 说明 |
  | --- | --- |
  | `ymodem-ota k` | 通过 YModem 接收 `*.bin`，写入 APP 槽并校验（升级 Kernel） |
  | `ymodem-ota b` | 自升级：把 Bootloader 镜像收到 APP 槽，校验后 5 秒确认，再从 RAM 拷贝覆盖自身 |
  | `run latest` | 校验并启动 APP |
  | `run bukup` | 单 APP 槽版本的说明性命令（无备份镜像可切） |
  | `bootinfo` | 打印 Flash 容量、分区、标志记录、向量表与 CRC 校验结果 |

* 等待传输 40 秒（每秒打印 `.` 并发送 `C`），可用 `Ctrl+C` 打断；
* **掉电安全**：只有“接收完成 + CRC 校验通过 + 向量表合法”后才写标志页，标志页用追加式记录（32 条），写入时先写日志页再落盘，掉电不会变砖。

Flash 布局（要求 128 KB 版本，64 KB 的 C8T6 只能放下单槽的裁剪配置）：

```
0x08000000 - 0x080057FF   Bootloader      22 KB
0x08005800 - 0x08005BFF   标志页           1 KB
0x08005C00 - 0x0800FFFF   APP 槽          41 KB
```

### 1.2 Kernel —— 三路虚拟串口 + 命令行

* USB Composite Device，电脑上同时出现 **3 个 COM 口**：
  * 口 1 / 口 2：与板载 UART2 / UART3 双向透传（波特率可被上位机改写）；
  * 口 3：letter-shell 命令行（替代原来的 UART1 交互）；
* 上电会先**主动断开/重连一次 D+ 线**，保证主机能重新枚举（板上有硬上拉电阻时的必要动作）；
* 命令行命令：

  | 命令 | 说明 |
  | --- | --- |
  | `pin pb5 in` / `pin pb5 out` | 配置 GPIO 为输入 / 输出 |
  | `pin read pb5` | 读电平（输入、输出模式都能读） |
  | `pin write pb5 1` | 输出高 / 低（**非输出模式会报错**） |
  | `pin pa13 out force` | 允许操作 SWD 引脚（默认拒绝，避免自锁） |
  | `led 5` | 设置 LED 闪烁频率（0 = 常亮） |

* 启动时会向 UART1 打印 `[app] ...` 跟踪信息，便于排查“跳转后没反应”。

> 详细说明、烧录命令、验证步骤见 [`USBto2xUARTtool/README.md`](USBto2xUARTtool/README.md)。

---

## 二、上位机 `SerialMateX`

基于 **Wails v2 + Go + Vue 3** 的桌面工具（前端为单文件 `frontend/src/App.vue`），左侧配置、中间收发、右侧命令面板：

* **SERIAL**：串口参数（端口 / 波特率 / 数据位 / 停止位 / 校验位）、Hex 显示、自动滚屏；
* **NETWORK**（左上角 `≡` 菜单）：TCP Client / TCP Server / UDP；
* **RX MONITOR**：终端式直接输入（`Enter` 只发一个回车、`↑/↓` 翻历史、`Backspace` 发 `0x7F`、`Ctrl+C` 发 `0x03`、选中可复制、`Ctrl+V` 直接把剪贴板发给下位机）；
* **TX EDITOR**：单行输入、回车发送、`↑/↓` 历史，底部细长按钮展开 / 收起；
* **AUTO CMD**（右侧栏）：
  * 每条命令带 **TX / RX 标志**（TX 绿色、RX 红色）与 **0~99 的间隔秒数**；
  * 支持功能键标记：`{ENTER}` `{LF}` `{TAB}` `{ESC}` `{UP}` `{DOWN}` `{LEFT}` `{RIGHT}` `{HOME}` `{END}` `{PGUP}` `{PGDN}` `{DEL}` `{INS}` `{BS}` `{SPACE}` `{0xNN}`，末尾未写回车会自动补 `\r`；
  * `新增`（选中时插到该条上方）/ `删除` / `导入`（读 `.txt` 覆盖列表），内容实时写入 `auto_cmd.txt`；
  * 底部 `启动` / `停止`：运行时按钮外围有顺时针匀速流光，停止时为呼吸灯；
* **TASK**（左侧 `TASK` 页）：
  * 设备选择（来自 `pwr_dev.ini`，可在弹窗里增删改：设备名 / COM 口 / PWR-ON / PWR-OFF 十六进制序列）；
  * 开关：`保存日志`、`初始状态`、`异常暂停`；参数：`运行时间` / `间隔时间` / `运行次数`（点击数字弹窗修改，实时写 `task_config.ini`）；
  * 启动后按“上电 → 逐条执行命令（RX 等匹配、TX 发送、按数字延时）→ 运行时间到掉电 → 等待间隔时间 → 下一轮”循环，共跑 `运行次数` 轮；
  * **TASK STATUS** 实时显示：当前轮次、正常次数、电源状态、`N 秒后上电/掉电`、当前电流；
  * `保存日志` 会把上电/掉电标识（`*` 包围）与被测设备串口内容写入 `DEBUGLOG_YYYY-MM-DD_HH-MM-SS.log`，写入即关闭，文件不被独占。

> 界面布局、配置文件格式、任务执行流程详见 [`SerialMateX/README.md`](SerialMateX/README.md)。
>
> 本项目基于开源项目 [TheWinds071/serial-mate](https://github.com/TheWinds071/serial-mate)（基线提交 `6d45ea5`）二次开发。

---

## 三、目录结构

```
AutoDebugSystem/
├── USBto2xUARTtool/              # 下位机固件
│   ├── F103-Bootloader/          #   串口升级引导（CubeMX + Makefile + letter-shell）
│   ├── Kernel/                   #   主程序（3 路虚拟串口 + shell + pin 命令）
│   ├── tools/                    #   烧录 / 串口 / YModem 辅助脚本（PowerShell）
│   └── dist/                     #   已编译的 Bootloader / APP 镜像
└── SerialMateX/                  # 上位机（Wails + Go + Vue3）
    ├── frontend/                 #   前端（Vue3 单文件）
    ├── pkg/                      #   J-Link 等底层封装
    └── *.go                      #   app.go / task.go / autocmd.go / pwrdev.go ...
```

`dist/` 下是可直接烧写的成品（`bootloader-*.bin`、`app-*.bin`、`flag.bin`）；`tools/` 里的 `flash.ps1`（OpenOCD/DAPLink 烧录）、`ymodem_send.ps1`（串口发镜像）在另一台电脑上同样可用（需要 PowerShell）。

---

## 四、编译

### 4.1 下位机（任意平台）

需要 `arm-none-eabi-gcc` 与 `make`：

```bash
cd USBto2xUARTtool/F103-Bootloader && make -j4     # 产出 build/F103-Bootloader.bin / .hex / .elf
cd ../Kernel && make -j4                            # 产出 build/USBto2xUARTtool.bin / .hex / .elf
```

* Bootloader 链接在 `0x08000000`（22 KB 区），APP 链接在 `0x08005C00`（41 KB 区），两者的 `.ld` 与 `boot.h` 常量一一对应；
* 修改分区后三处必须同步：`F103-Bootloader/Core/Inc/boot.h`、两个 `*_FLASH.ld`、`Kernel/Core/Src/system_stm32f1xx.c` 的 `VECT_TAB_OFFSET`。

### 4.2 上位机

需要 Go ≥ 1.23、Node ≥ 18、Wails v2.11：

```bash
go install github.com/wailsapp/wails/v2/cmd/wails@v2.11.0
cd SerialMateX
wails build            # Windows 产出 build/bin/SerialMateX.exe
```

* 首次构建会自动 `npm install` 与 `vue-tsc` 类型检查；改了 Go 侧导出方法后先 `wails generate module` 重新生成 `frontend/wailsjs/**` 绑定；
* Linux 需要额外的 `libgtk-3` / `webkit2gtk` 依赖（见 [`SerialMateX/README.md`](SerialMateX/README.md)）。

---

## 五、烧录与联调

1. `bootloader-*.bin` 烧到 `0x08000000`（ST-Link / OpenOCD / DAPLink）；
2. APP 可用 `ymodem-ota k` 经串口升级，或直接把 `app-*.bin` 烧到 `0x08005C00`（同时用 `flag.bin` 写入标志页，或用 `tools/flash.ps1`）；
3. 打开上位机 `SerialMateX`，在 `SERIAL` 页连下位机的第 3 个 COM 口，先发 `help` 确认命令行正常；
4. 切到 `TASK` 页配置设备与参数，`AUTO CMD` 里写好命令序列，点右下角 `启动` 开始自动测试。

> 已知限制：`保存功耗` 与 `当前电流` 在当前版本置为不可用（固定 `关` / `NULL`），接口与 UI 位置保留，后续接入采样后即可启用。
