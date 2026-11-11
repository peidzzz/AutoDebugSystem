# USBto2xUARTtool：Bootloader + Kernel

两个独立的 STM32F103C8T6（64 KB Flash / 20 KB RAM）工程，配合完成"串口下载 + 校验启动"和"USB 三路串口 + 命令行"。

| 工程 | 作用 | 烧录/链接地址 |
| --- | --- | --- |
| `F103-Bootloader` | 串口命令行（letter-shell on UART1）+ YModem 下载 + 启动倒计时 + 校验后跳 APP | `0x08000000` |
| `Kernel` | 原 USB↔UART2/UART3 桥 + 第三个虚拟 COM 口跑 letter-shell + `pin` 命令 | `0x08005C00`（由 Bootloader 跳转） |

## 1. 64 KB Flash 布局（量产目标：STM32F103C8T6）

```
0x08000000 .. 0x080057FF   Bootloader      22 KB   （当前 22 200 B，余 328 B）
0x08005800 .. 0x08005BFF   标志页          1 KB    （32 条记录，见 1.3）
0x08005C00 .. 0x0800FFFF   APP（唯一槽）   41 KB   （当前 41 700 B，余 284 B）
```

### 1.1 为什么只有一个 APP 槽

一份 `.bin` 的代码地址在链接时就定死了，只有链接地址处的那一份能执行。双槽方案需要
`2 × 41.4 KB + 21 KB ≈ 104 KB`，只有 128 KB 器件放得下；本版本面向必须量产的 C8T6（64 KB），
所以只留一个槽，改为把 **Bootloader 本身当作永久救援通道**：

1. 镜像直接收进唯一的 APP 槽；
2. 收完先做向量表校验（初始 SP 在 SRAM、复位向量为奇数且落在槽内），再做整片 CRC32 回读比对
   （接收时算一遍、读 Flash 再算一遍），两者一致才写标志记录；
3. 每次上电都按标志记录里的 `size + crc32` 再校验一次，**校验不过绝不跳转**；
4. 传输中掉电最多让槽里留下残镜像，Bootloader 永远不会被写，所以设备仍能应答命令行、重发即可，**不会变砖**。

| 掉电时刻 | 结果 |
| --- | --- |
| 擦除/接收过程中 | 槽里是残镜像，CRC 对不上 → 下次上电停在 Bootloader，重新 `ymodem-ota k` 即可 |
| 收完、校验中 | 同上（记录还是旧的，CRC 已变） |
| 校验通过、写记录前 | 同上 |
| 记录写完 | 下次启动校验通过 → 直接跳 APP |

代价（必须清楚）：**单槽没有自动回滚**。一旦开始写入，旧镜像就被覆盖了；这是 64 KB 器件的硬约束。
需要"升级失败自动退回上一版"就必须换 128 KB 器件，并恢复双槽 + 搬运校验方案。

### 1.2 容量预算

| 项目 | 字节 | 说明 |
| --- | --- | --- |
| Bootloader 区域 | 22 528（22 KB） | `STM32F103C8Tx_FLASH.ld` 写死 `LENGTH = 22K`，超了直接链接失败，不会顶到标志页 |
| Bootloader 实际 | 22 200 | `text 21 888 + data 308`（其中 300 B 是 RAM 里运行的更新搬移程序），余 **328 B** |
| 标志页 | 1 024 | 32 × 32 B 记录 |
| APP 槽 | 41 984（41 KB） | `ORIGIN = 0x08005C00, LENGTH = 41K` |
| APP 实际 | 41 956 | `text 41 440 + data 512`，余 **28 B** |

RAM（20 KB）：APP `.data 512 + .bss 16 904 + 堆栈预留 1 536 = 18 952 B`；Bootloader `308 + 5 424 + 1 536 = 7 268 B`。
两者不同时运行，各自都在 20 KB 内。

> 两个余量都不大（Bootloader 328 B、APP 只剩 **28 B**）。加功能前后都跑一次
> `arm-none-eabi-size build/*.elf`，别等到链接报 `region FLASH overflowed`。
> Bootloader 已经因此关掉了 letter-shell 的 ASCII banner（`SHELL_SHOW_INFO 0`，省约 400 B），
> 换成一行 `console ready, 'help' lists the commands`。APP 这边 28 B 什么也放不下，
> 下一个功能必须先删掉 `App_EarlyTrace()` 的几行启动跟踪再往里加。

### 1.3 标志记录（append-only）

标志页按 32 字节一条顺序追加：`magic("BOOT") / version / seq / state / slot / image_size / image_crc / 自身CRC`。
每次升级写在"上一条之后第一个空白记录"处，**不擦页**，所以写记录过程中掉电不会破坏上一条。
32 条用满后才整页擦一次（此时最新记录一起消失，下一条仍会描述当前镜像；真丢记录时启动只做向量表校验）。

`bootinfo` 会打印：器件报告的 Flash 容量、布局、最新记录、向量表、以及"回读 CRC 是否与记录一致"。
`flash` 一栏读的是芯片内部的 `F_SIZE`（`0x1FFFF7E0`），所以能立刻看出手上是不是真的 128 KB 片子。

## 2. Bootloader（命令行在 UART1 = PC 的 COM9，115200 8N1）

### 2.1 启动流程

1. 打印构建时间：`bootloader build Oct 22 2026 22:36:03`（`main.c` 的 `__DATE__/__TIME__`，重新编译就变；
   这是判断固件新旧的**唯一**可靠依据）；
2. 打印 `bootloader: press any key within 3`，并在同一位置倒计时 `3`→`2`→`1`（退格覆盖，不换行）；
   期间按任意键 → 直接进命令行；
3. 紧接着打印 `reset cause: power-on`（读 `RCC->CSR` 的复位标志：`power-on` / `the reset pin` /
   `software` / `watchdog` / `low power`，打印后清标志）——判断"跳转后到底重启了没有、为什么重启"就看这一行；
4. 无按键 → 校验（向量表 + 记录里的 CRC）→ 跳 APP（`Boot_JumpToApp()`：关 UART1/SysTick/全部 NVIC，
   重设 `VTOR = 0x08005C00`、MSP，再跳到 APP 复位向量；**不再** `HAL_RCC_DeInit()`，时钟树交给 APP 自己配）；
5. 校验不过 → 打印原因并进入命令行（`no application image found` / `does not match the recorded checksum`）。

### 2.2 命令

| 命令 | 说明 |
| --- | --- |
| `ymodem-ota k` | 下载/升级**内核**（收进 APP 槽 `0x08005C00`）。**40 秒**内每秒打印一个 `.` 并发一次 `'C'`，收到任何字节立即开始接收；**等待期间随时可以按 `Ctrl+C` 取消**（打印 `update cancelled with control-C.`）；40 秒无数据则 `no transfer started, update cancelled.`。只接受 `.bin`。 |
| `ymodem-ota b` | 升级 **bootloader**：新镜像先收到 APP 槽并校验（向量表必须落在 `0x08000000..0x080057FF`），再给 5 秒"按任意键取消"窗口，之后由**在 RAM 里运行**的搬移程序擦写 `0x08000000` 并复位。搬移过程中掉电只能用 SWD 救；升完后 APP 槽里是 bootloader 镜像，需要再 `ymodem-ota k` 重发内核。 |
| `ymodem-ota`（无参数/参数不对） | 只打印用法 `usage: ymodem-ota k (kernel) | ymodem-ota b (bootloader)`，不再默认等于 `k`。 |
| `run latest` | 校验后立即启动当前镜像（等同"跳过倒计时"）。 |
| `run bukup` / `backup` | 保留该命令：单槽没有备份镜像可切，会提示 `one slot only (64 KB part): there is no backup image` 并保持命令行。 |
| `bootinfo` | 打印 Flash 容量、布局、最新标志记录、向量表与 CRC 校验结果。 |
| `help` / `clear` / `keys` | letter-shell 自带；`Tab` 补全由 `Shell_Loop()` 直接调用 `shellTab()`，不依赖按键表。 |

### 2.3 下载流程里的两道保护

- **先验后擦**：收到第一个数据包（含向量表）就按"初始 SP 在 SRAM、复位向量为奇数且在 `0x08005C00..0x0800FFFF`"
  检查，不合格立刻 CAN 取消并提示 —— 这时**一个字节都还没擦**，已安装的 APP 完好无损。
  最常见的触发场景是把 `bootloader-*.bin` 当 APP 发（它链接在 `0x08000000`）。
- **按页惰性擦除**：不再一开始就把整个槽擦掉，而是 `Boot_WriteAppChunk()` 写到哪里、擦到哪一页（每页约 20 ms），
  发送端不会因为长时间擦除而超时，也避免了"文件不对却把旧程序擦没了"。

### 2.4 实现要点

- **UART1 不用 HAL 驱动**：`Core/Src/usart.c` 直接操作寄存器（`PA9` AF_PP、`PA10` 浮空输入、
  `BRR = SystemCoreClock/baud`、`RXNEIE`），中断里把字节交给 `Shell_RxByte()`。省下
  `stm32f1xx_hal_uart.c + stm32f1xx_hal_dma.c` 约 2.3 KB —— 没有这部分余量，21 KB 区域装不下。
- 接收在倒计时之前就开始（`Shell_RxInit()`），否则"倒计时期间按键"可能丢字节。
- 无 RTOS：`Shell_Loop()` 在主循环里轮询 `shellHandler()`（`SHELL_TASK_WHILE 0`、`SHELL_USING_LOCK 0`）。
- 命令表用链接段 `shellCommand`（`.ld` 里 `KEEP(*(shellCommand))` + `_shell_command_start/end`），
  所以 `ymodem-ota`（带连字符、不是合法 C 标识符）是手写的表项，`run`/`bootinfo` 用 `SHELL_EXPORT_CMD`。
- `SHELL_EXPORT_CMD` 的坑：结尾必须带分号；描述里不能有逗号。
- `Tab` 补全：`Shell_Loop()` 截到 `0x09` 直接调 `shellTab()`，不再依赖 letter-shell 的按键表
  （按键表里确实有 tab 项，但走自己的分支最稳）。
- `ymodem-ota b` 的搬移程序（`Boot_CopyStub`）放在 `.data`，即运行在 SRAM：擦除自己所在的 Flash 页时
  代码不会消失。它只操作寄存器、不调用任何函数（所有函数都在被擦的 Flash 里）。
- 编译用 `-Os`，`FLASH` 限制 22 KB。

### 2.5 跳转后没输出怎么查

Bootloader 每次启动打印 `reset cause: <原因> csr 0x????????`（原始 `RCC->CSR`，读完清标志），
APP 侧最早的四行是寄存器级打印（`App_EarlyTrace()`，不依赖 HAL/时钟配置）：

```
[app] startup reached (Reset_Handler) <- 复位向量落地（在 SystemInit 之前打印）
[app] reset vector reached main      <- 跳转成功、Reset_Handler 跑完
[app] HAL_Init ok
[app] clock setup ok
[app] GPIO/DMA/CRC/UARTs ok
[app] build Oct 22 2026 22:36:05, clock and UARTs ready
```

| 现象 | 结论 |
| --- | --- |
| `starting application ...` 后连 `[app] startup reached` 都没有 | 复位向量没落地，或那一刻电源/复位被硬件拉掉（看 `csr`） |
| 有 `[app] startup reached` 但后面没有 `reset vector reached main` | 卡在 `SystemInit()`（时钟切换）里 |
| 只到 `[app] HAL_Init ok` / `clock setup ok` 就断 | 卡在该初始化里 |
| 能看到全部 `[app]` 行 | APP 已正常启动，问题在 USB / FreeRTOS 之后 |
| `csr 0x0C000000`（POR+PIN） | 正常上电 |
| `csr 0x04000000`（只有 PIN） | 纯复位脚复位：复位按钮 / 串口模块的 DTR-RTS 自动复位 / 烧写器 nRST |
| `csr 0x14000000`（SFT+PIN） | 有代码主动复位（两份固件里都没有 `NVIC_SystemReset`） |

## 3. Kernel（USB 三个虚拟 COM 口）

原工程 2 个 COM（USB↔UART2、USB↔UART3）；现在第 3 个 COM 口接 letter-shell，**UART1 不再作为控制台**
（`bridge.c` 里 USART1 分支已移除，`huart1` 只给 `App_BootLog()` 打启动跟踪）。
三个端口依次是 interface 0/1、2/3、4/5，Windows 的 COM 号由枚举顺序决定、**不是固定的**，
所以认口请用 `pnputil /enum-devices /connected /class Ports` 看 `VID_0483&PID_5740&MI_0x`：
答 `help` 的那个就是 shell（本机实测是 `MI_04` → COM10）。

### 3.0 为什么"上电时 Windows 认不到板子"（已定位并修好）

板子 D+ 上有一颗**硬连的 1.5 k 上拉**（用 SWD 把 USB 收发器 PDWN 置位后 `GPIOA->IDR` 的 PA12 仍为高，
证明不是芯片内部上拉）。于是从加电那一刻——也就是 bootloader 还在 3 秒倒计时、根本没人应答
描述符请求的时候——主机就已经看到一个"设备"。Windows 枚举失败后会把端口禁用，而 D+ 全程不掉，
它也就不会重试，最终显示"未知 USB 设备（设备描述符请求失败）"。

`App_Init()` 开头因此先调用 `App_UsbReconnect()`：把 PA12 拉低 200 ms 强制断开，再放回浮空输入，
让 D+ 重新上拉、主机重新枚举。实测三个 COM 口随即出现（`reset cause` 里也能看到对应的复位）。

### 3.0.1 端点与 PMA（512 B 是硬上限）

第 3 口端点 `0x85`（bulk IN，8 字节包）、`0x03`（bulk OUT，8 字节包）、`0x86`（interrupt）。
PMA 每个 bulk OUT 缓冲按 32 字节取整，所以 shell 口用 8 字节包（正好一块）才能把整张表塞进
`0x40..0x1FF` 这 448 字节：`EP0 64+64 + 两个桥 2×64 out + shell 32 + 桥 in 3×64 + 4×8 = 448`。
控制台不在乎带宽，8 字节包不影响手感。

另外 F1 的 USB 核**按端点号**共用寄存器，本工程里 `0x83`（桥 2 的 bulk IN）和 `0x03`（shell 的 bulk OUT）
号相同，所以"端点→端口"的解析必须**分方向**做（`PortFromInEp` / `PortFromOutEp`）；早先只看号不看方向，
shell 的输入被送进了 UART3，`0x03` 永远没人应答 —— 表现就是"COM10 能打不能收、写超时"。

### 3.1 `pin` 命令（`Core/Src/pin.c`）

| 命令 | 说明 |
| --- | --- |
| `pin pb5 out` | 推挽输出（`pb5`/`b5`/`PB5` 均可，端口 a..e，引脚 0..15） |
| `pin pb5 in` | 输入（无上下拉） |
| `pin read pb5` | 读电平，输入/输出都可用，打印 `pb5 = 1 (high, output)` |
| `pin write pb5 1` | **仅输出模式**可用；输入模式报错并提示先 `pin pb5 out`；值只接受 0/1 |
| `pin pa13 out force` | `pa13/pa14` 是 SWD 的 SWDIO/SWCLK，默认**拒绝**改（改了探针就再也连不上）；确实要当普通 IO 用才加 `force` |
| `pin` | 用法 |

方向从 `GPIOx->CRL/CRH` 回读，所以对别处配置过的引脚也判得对；PA15、PB3/PB4（JTAG 专用脚）
会打印 `warning: ... used by JTAG`，PA13/PA14 则直接报错拒绝（见上表 `force`）。

### 3.2 启动跟踪

APP 的控制台在第 3 个虚拟 COM 口，跳过去以后 UART1 会安静下来，容易误判成"没起来"。
`app.c` 的 `App_BootLog()` 会在 **UART1（115200）** 打启动跟踪（`APP_BOOT_TRACE 0` 可整体关掉）：

```
[app] build Oct 22 2026 21:31:05, clock and UARTs ready
[app] App_Init: USB ...
[app] USB started, bridge ...
[app] bridge ready, LED ...
[app] LED ready, shell ...
[app] shell ready: console on the 3rd virtual COM port
```

停在哪行就说明卡在哪步。`PC13` LED 是另一路与串口无关的指示：APP 起来后先连闪 3 下
（`App_BlinkBurst(3)`），随后 500 ms 周期闪烁。注意 **Bootloader 启动时就会把 PC13 拉低**，
所以"LED 常亮"在 Bootloader 阶段是正常现象，不能用来判断 APP 出错；
`configASSERT` 失败和 `Error_Handler()` 都会把 LED 拉低后死循环。

## 4. 编译

```bash
cd USBto2xUARTtool/F103-Bootloader && make -j4
cd USBto2xUARTtool/Kernel          && make -j4
```

工具链：`arm-none-eabi-gcc` 9.2.1（`D:\EmbeddedDevelopTools\GNU-Tools-ARM-Embedded\bin`）。
`make clean` 里的 `rm -fR build` 在本机没有 `rm` 时会失败，需要彻底重编时用
`Remove-Item -Recurse -Force build`（PowerShell）。

产物：`F103-Bootloader/build/F103-Bootloader.bin`、`Kernel/build/USBto2xUARTtool.bin`，
以及按时间命名的副本目录 `dist/`（`dist/obsolete/` 是历史版本，不要用）。

计算某个 `.bin` 的 CRC32（与 bootloader 里的算法一致，用来和 `ymodem-ota` 打印的值比对）：

```
python tools/crc32.py dist/app-20261022-2131.bin
```

## 5. 烧录

### 5.1 `tools/flash.cmd`（推荐）

```
tools\flash.cmd combined              :: DAPLink 拖拽盘：Bootloader + APP 一次写完
tools\flash.cmd bootloader            :: 只写 Bootloader（0x08000000）
tools\flash.cmd app -Via openocd      :: 用 OpenOCD 只写 APP（0x08005C00）
tools\flash.cmd -Erase -Via openocd   :: 全片擦除
tools\flash.cmd combined -DryRun      :: 只打印将要写入的镜像和 crc32，不动硬件
```

脚本自动挑 `dist\` 里最新的 `bootloader-*.bin` / `app-*.bin`，打印与 Bootloader 完全相同算法
（MSB-first、多项式 `0x04C11DB7`）的 crc32，可以和 `ymodem-ota` 打印的值直接对照。
`-Via daplink` 靠探针的拖拽盘（`J:\`），所以只能从 `0x08000000` 开始写：选 `app` 时脚本会自动拼一个
`dist\combined-*.bin`（Bootloader + `0xFF` 填充到 `0x08005C00` + APP）。注意这会顺带把**标志页擦成空白**，
Bootloader 下次启动会打印 `flag: empty`，然后按向量表 + CRC 判断能否跳转。
`-Via openocd` 会写入指定地址，脚本会自动选机器上版本最高的 OpenOCD（见 5.2）。

### 5.2 关于 DAP 下载器（已实测，结论重要）

板子上的 DAP 是 **DAPLink**（`VID_0D28&PID_0204`，MSD + CDC + HID + WebUSB 四个接口），
`COM9` 就是它自己的虚拟串口 —— 所以 UART1 的调试信息从 COM9 出来，不需要额外的 USB-TTL。

实测结果（OpenOCD 0.11.0 与 ESP-IDF 自带的 0.12.0 都试过）：

| 通道 | 结果 |
| --- | --- |
| `hid`（CMSIS-DAP v1） | **可用**：0.11 写 `cmsis_dap_backend hid`、0.12 写 `cmsis-dap backend hid`，都能立刻连上（`SWD DPIDR 0x1ba01477`）。`tools\flash.cmd -Via openocd` 默认就用它 |
| `usb_bulk`（WebUSB / CMSIS-DAP v2） | 两个版本都是 `Error: unable to find a matching CMSIS-DAP device` |
| 拖拽盘（MSD） | `FAIL.TXT: error: An internal error has occurred / type: internal` |

> **拖拽盘会先把芯片擦干净再失败**。本项目就被它擦成过空白片：上电没串口、Windows 报
> "未知 USB 设备（设备描述符请求失败）"，用 SWD 一读 `0x08000000`/`0x08005C00` 全是 `0xFFFFFFFF`。
> 所以**不要用拖拽盘**，也不要用 `flash.cmd combined`（它走的正是拖拽盘）。
> 救砖方法：`tools\flash.cmd combined -Via openocd`（见 5.1）。

如果 OpenOCD 连不上，按这个顺序排查：

1. SWD 四根线是否接好：`SWDIO→PA13`、`SWCLK→PA14`、`GND`，探针若给板子供电再接 `3V3`。
2. 板子是否真的上电（量 `3.3V`），探针的灯是否常亮/呼吸。
3. 是否还有别的程序占着 HID 口（Keil、pyOCD、mbed Studio 之类）。
4. `pin pa13 out force` / `pin pa14 out force` 会把 SWD 脚变成普通 IO —— 掉电重启即可恢复（复位后默认是 SWD）。

如果只是要更新 APP，**不用探针**：`ymodem-ota k` 走 UART1 一样能升级。

### 5.3 ST-Link / STM32CubeProgrammer

```
STM32_Programmer_CLI -c port=SWD -e all \
  -w dist/bootloader-20261022-2241.bin 0x08000000 \
  -w dist/app-20261025-2152.bin 0x08005C00 -v
```

`-e all` 会连标志页一起擦干净。之后每次升级只需要 `ymodem-ota k`（发 `dist/app-*.bin`），
Bootloader 不用重新烧。

### 5.4 调试/下载脚本一览（`tools\`）

| 脚本 | 用途 |
| --- | --- |
| `flash.cmd` | SWD 烧写（`-Image combined\|bootloader\|app`、`-Via openocd\|daplink`、`-Erase`、`-DryRun`、`-FlagRecord`） |
| `ymodem_send.cmd` | 一条命令完成 OTA：自动打断倒计时、发 `ymodem-ota k\|b`、用 YModem 发 `.bin`、打印结果 |
| `bootshell.ps1` | 抓 Bootloader 命令行：连续发回车打断倒计时，然后执行指定命令 |
| `shell.ps1` / `shell.cmd` | 往任意虚拟 COM 口发命令并打印回复（`-Port COM10 -Commands 'help','pin pb5 out'`） |
| `comlog.ps1` / `comlog.cmd` | 记录串口日志（运行中每 500 ms 落盘，掉线也不丢） |
| `term.ps1` | 简易终端（`-Port -Commands -WaitMs`），无交互界面时够用 |
| `ocd.ps1` | 跑 OpenOCD 并用 `log_output` 收集输出（直接重定向 stdout 会丢输出） |
| `bootflag.ps1` | 离线生成 32 字节标志记录（`-Bin app.bin -Seq N`），配合 `flash.cmd -FlagRecord` |
| `crc32.py` | 与 `Boot_Crc32` 完全一致的 crc32，用来核对 `ymodem-ota` 打印的值 |
| `openocd\dap-stm32f1.cfg` | DAPLink + STM32F1 的 OpenOCD 配置（HID 后端） |

例：

```
tools\ymodem_send.cmd -Port COM9 -File dist\app-20261026-2151.bin
tools\flash.cmd app -Via openocd -FlagRecord dist\flag.bin
tools\shell.cmd -Port COM10 -Commands 'pin pb5 out','pin write pb5 1','pin read pb5'
```

## 6. 验证清单（2026-10-26 已在本机硬件上逐条跑过）

| # | 项目 | 结果 |
| --- | --- | --- |
| 1 | 上电 → `bootloader build ...`、`reset cause: ...`、`press any key within 3`（原地覆盖 `3`→`2`→`1`） | 通过 |
| 2 | 倒计时期间按任意键 → `command line requested` → `console ready, ...` → `letter:/$`（提示符在提示语之后） | 通过 |
| 3 | 不按键 → `starting application ...` → UART1 打出 `[app] reset vector reached main` … `[app] shell ready` | 通过 |
| 4 | 三个虚拟 COM 口出现（实测 `MI_00`→桥 1、`MI_02`→桥 2、`MI_04`→shell），shell 口 `help`/`pin`/`led` 正常 | 通过 |
| 5 | `pin pb5 out` → `pin write pb5 1` → `pin read pb5` → `pin pb5 in` → `pin write pb5 1` 报错 `pb5 is an input` → `pin read pb5` 仍可读 | 通过 |
| 6 | `pin pa13 out` 被拒（`is the SWD link`），加 `force` 才执行 | 通过 |
| 7 | `ymodem-ota`（无参/错参）只打印用法；`ymodem-ota k` 打点倒计时；等待中 `Ctrl+C` → `update cancelled with control-C.` | 通过 |
| 8 | `tools\ymodem_send.cmd` 发 41 956 B 的 `app-*.bin`，6.4 s 完成 → `crc32 0x9272F203` + `download verified and recorded` | 通过 |
| 9 | `run latest` → 跳转并跑起 APP（`[app]` 跟踪完整） | 通过 |
| 10 | `ymodem-ota b` 发 `bootloader-*.bin` → 收进 APP 槽校验 → 5 秒确认 → `copying, the device will reset ...` → 复位后 Bootloader 正常 | 通过 |
| 11 | 把 `bootloader-*.bin` 当内核发 → `the file is not an image for this slot (-7)`，原有 APP 未被擦除 | 通过 |
| 12 | `bootinfo` → 容量/布局/记录/向量表/`crc: matches the record` | 通过 |
| 13 | `Tab` 补全（`pi` + Tab → `pin `） | 通过 |
| 14 | 升级到一半复位 → 停在 Bootloader、CRC `MISMATCH`，重发一次即恢复 | 逻辑保证（未做破坏性实测） |

## 7. 已知限制

- 必须 64 KB 及以上的 F103（布局按 64 KB 排满）；`bootinfo` 会打印器件自报容量。
- 单槽无自动回滚（见 1.1）。
- Bootloader 的命令行与 YModem 都在 UART1（PA9/PA10）。现在 UART1 接在 DAPLink 探针的虚拟串口上，
  所以 PC 端看 **COM9** 就够，不需要额外 USB-TTL（换板子/换探针时才需要）。
- APP 起来后控制台在第三个虚拟 COM 口，UART1 只输出 `[app]` 启动跟踪。
- 标志页最多 32 条记录，之后整页擦除重来；极端情况下（擦除瞬间掉电）会退化为"只做向量表校验"。
- Bootloader 只剩 328 B、APP 只剩 28 B，加功能前先看链接结果。
- shell 口（第 3 个 COM）的 bulk 包是 8 字节：控制台够用，但不要拿它传大文件。
- 探针用 HID 后端可以下载（见 5.2）；拖拽盘会先把芯片擦掉，**不要用**。
