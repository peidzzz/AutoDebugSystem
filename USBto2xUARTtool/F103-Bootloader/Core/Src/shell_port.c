/**
  ******************************************************************************
  * @file    shell_port.c
  * @brief   letter-shell console on USART1 and the bootloader commands.
  *
  * The bootloader is single threaded: received bytes are collected in a ring
  * buffer by the USART1 interrupt and fed to shellHandler() from the main
  * loop, so no shellTask() is needed and the same ring also serves the YModem
  * receiver.
  ******************************************************************************
  */

/* Includes ------------------------------------------------------------------*/
#include "shell_port.h"
#include "boot.h"
#include "ymodem.h"
#include "main.h"
#include "usart.h"

#include <string.h>

/* Private defines -----------------------------------------------------------*/
#define SHELL_RX_RING_SIZE      2048U
#define SHELL_RX_RING_MASK      (SHELL_RX_RING_SIZE - 1U)
#define SHELL_BUFFER_SIZE       512U
#define SHELL_UART_TIMEOUT_MS   1000U
#define SHELL_OTA_START_SECONDS 40U

/* Private variables ---------------------------------------------------------*/
static Shell shell;
Shell *shellConsole = NULL;

static char shellBuffer[SHELL_BUFFER_SIZE];
static uint8_t shellRxRing[SHELL_RX_RING_SIZE];
static volatile uint16_t shellRxHead = 0U;
static volatile uint16_t shellRxTail = 0U;

/* Private function prototypes -----------------------------------------------*/
static signed short shellWrite(char *data, unsigned short length);
static signed short shellRead(char *data, unsigned short length);
static int shellCmd_ymodem_ota(int argc, char *argv[]);
static int shellCmd_run(int argc, char *argv[]);
static int shellCmd_bootinfo(int argc, char *argv[]);

/* Exported commands ---------------------------------------------------------*/
/* The command is called "ymodem-ota", which is not a valid C identifier, so the
   table entry is spelled out instead of using SHELL_EXPORT_CMD. */
SHELL_USED const ShellCommand shellCommandYmodemOta SHELL_SECTION("shellCommand") =
{
  .attr.value = SHELL_CMD_PERMISSION(0) | SHELL_CMD_TYPE(SHELL_TYPE_CMD_MAIN) |
                SHELL_CMD_DISABLE_RETURN,
  .data.cmd.name = "ymodem-ota",
  .data.cmd.function = (int (*)())shellCmd_ymodem_ota,
  .data.cmd.desc = "receive an application with YModem (.bin) and record it",
};

SHELL_EXPORT_CMD(SHELL_CMD_PERMISSION(0) | SHELL_CMD_TYPE(SHELL_TYPE_CMD_MAIN) | SHELL_CMD_DISABLE_RETURN, run, shellCmd_run, run latest or run bukup to start an application);
SHELL_EXPORT_CMD(SHELL_CMD_PERMISSION(0) | SHELL_CMD_TYPE(SHELL_TYPE_CMD_MAIN) | SHELL_CMD_DISABLE_RETURN, bootinfo, shellCmd_bootinfo, show the flash layout and the boot flag);

/* Private functions ---------------------------------------------------------*/

static signed short shellWrite(char *data, unsigned short length)
{
  if (length == 0U)
  {
    return 0;
  }

  Usart1_Write((const uint8_t *)data, length);
  return (signed short)length;
}

static signed short shellRead(char *data, unsigned short length)
{
  uint32_t index;

  for (index = 0U; index < length; index++)
  {
    if (Shell_ReadByte((uint8_t *)&data[index], SHELL_UART_TIMEOUT_MS) == 0)
    {
      break;
    }
  }

  return (signed short)index;
}

static int shellArgIs(const char *argument, const char *expected)
{
  return (strcmp(argument, expected) == 0) ? 1 : 0;
}

static const char *shellBootStateName(uint32_t state)
{
  return (state == BOOT_STATE_IDLE) ? "ready" : "unknown";
}

void Shell_RxByte(uint8_t byte)
{
  uint16_t next = (uint16_t)((shellRxHead + 1U) & SHELL_RX_RING_MASK);

  if (next == shellRxTail)
  {
    /* Ring full: drop the byte, the protocol layer retries. */
    return;
  }

  shellRxRing[shellRxHead] = byte;
  shellRxHead = next;
}

int Shell_HasByte(void)
{
  return (shellRxHead != shellRxTail) ? 1 : 0;
}

int Shell_PeekByte(uint8_t *byte)
{
  if ((byte == NULL) || (shellRxHead == shellRxTail))
  {
    return 0;
  }

  *byte = shellRxRing[shellRxTail];
  return 1;
}

void Shell_Flush(void)
{
  shellRxTail = shellRxHead;
}

int Shell_ReadByte(uint8_t *byte, uint32_t timeoutMs)
{
  uint32_t start = HAL_GetTick();

  if (byte == NULL)
  {
    return 0;
  }

  for (;;)
  {
    if (shellRxHead != shellRxTail)
    {
      *byte = shellRxRing[shellRxTail];
      shellRxTail = (uint16_t)((shellRxTail + 1U) & SHELL_RX_RING_MASK);
      return 1;
    }

    if ((HAL_GetTick() - start) >= timeoutMs)
    {
      return 0;
    }

    HAL_Delay(1U);
  }
}

void Shell_WriteRaw(const uint8_t *data, uint32_t len)
{
  if ((data == NULL) || (len == 0U))
  {
    return;
  }

  Usart1_Write(data, len);
}

void Shell_RxInit(void)
{
  shellRxHead = 0U;
  shellRxTail = 0U;

  /* Usart1_Init() enables the peripheral, the PA9/PA10 pins and the RX
     interrupt; every received byte lands in Shell_RxByte(). */
  Usart1_Init();
}

void Shell_Init(void)
{
  Shell_RxInit();

  shell.read = shellRead;
  shell.write = shellWrite;

  shellInit(&shell, shellBuffer, (unsigned short)SHELL_BUFFER_SIZE);
  shellConsole = &shell;
}

/* letter-shell registers the tab key in its command table, but the completion
   routine is reached here directly so it keeps working whatever the key table
   does.  Both paths end up in the same function. */
void shellTab(Shell *shell);

void Shell_Loop(void)
{
  uint8_t byte;

  for (;;)
  {
    if (Shell_ReadByte(&byte, 10U) != 0)
    {
      if (byte == 0x09U)
      {
        shellTab(&shell);
        continue;
      }

      shellHandler(&shell, (char)byte);
    }
  }
}

/**
  * @brief  Read the flash size in KByte from the factory information area.
  */
static uint32_t shellFlashSizeKb(void)
{
  return Boot_FlashSizeKb();
}

/* Commands ------------------------------------------------------------------*/

static int shellCmd_ymodem_ota(int argc, char *argv[])
{
  BootFlag flag;
  YmodemInfo info;
  uint32_t seconds;
  uint32_t limit;
  uint32_t execBase;
  uint32_t execSize;
  int isBootloader;
  int hasFlag;
  int result;
  int started = 0;

  if (argc != 2)
  {
    Shell_Printf("usage: ymodem-ota k (kernel) | ymodem-ota b (bootloader)\r\n");
    return 0;
  }

  if (shellArgIs(argv[1], "k") != 0)
  {
    isBootloader = 0;
  }
  else if (shellArgIs(argv[1], "b") != 0)
  {
    isBootloader = 1;
  }
  else
  {
    Shell_Printf("usage: ymodem-ota k (kernel) | ymodem-ota b (bootloader)\r\n");
    return 0;
  }

  /* Both files are received into the application slot: for the kernel it is the
     destination, for the bootloader it is the only place with spare room. */
  limit = (isBootloader != 0) ? BOOT_BOOTLOADER_SIZE : BOOT_APP_SIZE;
  execBase = (isBootloader != 0) ? BOOT_FLASH_BASE : BOOT_APP_ADDR;
  execSize = (isBootloader != 0) ? BOOT_BOOTLOADER_SIZE : BOOT_APP_SIZE;

  Shell_Printf("\r\nflash: %u KB reported by the device, layout needs %u KB\r\n",
               (unsigned int)shellFlashSizeKb(),
               (unsigned int)Boot_RequiredFlashKb());

  if (Boot_RangeInFlash(BOOT_APP_ADDR, limit) == 0)
  {
    Shell_Printf("error: the slot does not fit into the flash of this device\r\n");
    return 0;
  }

  hasFlag = Boot_ReadFlag(&flag);

  if (isBootloader != 0)
  {
    Shell_Printf("bootloader update: image received into 0x%08X, verified, then\r\n"
                 "copied over 0x%08X (%u bytes max, link address 0x%08X)\r\n",
                 (unsigned int)BOOT_APP_ADDR, (unsigned int)BOOT_FLASH_BASE,
                 (unsigned int)BOOT_BOOTLOADER_SIZE, (unsigned int)BOOT_FLASH_BASE);
    if (*(__IO uint32_t *)BOOT_APP_ADDR != 0xFFFFFFFFU)
    {
      Shell_Printf("warning: the kernel in the slot is overwritten; send it again\r\n"
                   "with 'ymodem-ota k' once the new bootloader runs\r\n");
    }
  }
  else
  {
    Shell_Printf("application slot at 0x%08X, %u bytes max\r\n",
                 (unsigned int)BOOT_APP_ADDR, (unsigned int)BOOT_APP_SIZE);

    if (Boot_ImageValid(BOOT_APP_ADDR) != 0)
    {
      if ((hasFlag != 0) &&
          (Boot_ImageCrc(BOOT_APP_ADDR, flag.image_size) == flag.image_crc))
      {
        Shell_Printf("warning: this overwrites the installed application\r\n"
                     "(there is no backup slot)\r\n");
      }
      else
      {
        Shell_Printf("note: the slot does not match the recorded crc, replacing it\r\n");
      }
    }
    else if (*(__IO uint32_t *)BOOT_APP_ADDR == 0xFFFFFFFFU)
    {
      Shell_Printf("note: the slot is empty, this is a first download\r\n");
    }
  }

  Shell_Printf("send the .bin within %u s (control-C cancels)\r\n",
               (unsigned int)SHELL_OTA_START_SECONDS);

  for (seconds = 0U; seconds < SHELL_OTA_START_SECONDS; seconds++)
  {
    uint32_t elapsed = 0U;
    uint8_t peek;

    /* Prompt a sender that waits for the receiver to ask for data. */
    Shell_WriteRaw((const uint8_t *)"C", 1U);
    Shell_Printf(".");
    if (((seconds + 1U) % 20U) == 0U)
    {
      Shell_Printf("\r\n");
    }

    while (elapsed < 1000U)
    {
      if (Shell_PeekByte(&peek) != 0)
      {
        if (peek == 0x03U)
        {
          uint8_t discarded;

          (void)Shell_ReadByte(&discarded, 0U);
          Shell_Printf("\r\nupdate cancelled with control-C.\r\n");
          Shell_Flush();
          return 0;
        }

        /* Anything else is the first byte of the transfer: leave it in the ring
           so the receiver sees the header of the first packet. */
        started = 1;
        break;
      }
      HAL_Delay(10U);
      elapsed += 10U;
    }

    if (started != 0)
    {
      break;
    }
  }

  if (started == 0)
  {
    Shell_Printf("\r\nno transfer started, update cancelled.\r\n");
    return 0;
  }

  Shell_Printf("\r\nreceiving ...\r\n");

  memset(&info, 0, sizeof(info));
  result = Ymodem_Receive(&info, limit, execBase, execSize);

  if (result != YMODEM_OK)
  {
    const char *reason = "unknown error";

    switch (result)
    {
      case YMODEM_ERR_TIMEOUT:  reason = "timeout"; break;
      case YMODEM_ERR_CANCEL:   reason = "cancelled"; break;
      case YMODEM_ERR_PROTOCOL: reason = "protocol error"; break;
      case YMODEM_ERR_FLASH:    reason = "flash write failed"; break;
      case YMODEM_ERR_SIZE:     reason = "size mismatch or image too large"; break;
      case YMODEM_ERR_NAME:     reason = "the file name must end with .bin"; break;
      case YMODEM_ERR_IMAGE:    reason = "the file is not an image for this region"; break;
      default: break;
    }

    Shell_Printf("update failed: %s (%d)\r\n", reason, result);

    if (result == YMODEM_ERR_IMAGE)
    {
      Shell_Printf("its vector table does not point into 0x%08X..0x%08X; nothing\r\n"
                   "was erased.\r\n",
                   (unsigned int)execBase,
                   (unsigned int)(execBase + execSize - 1U));
    }
    else if (result != YMODEM_ERR_CANCEL)
    {
      Shell_Printf("the slot is incomplete, send it again.\r\n");
    }

    return 0;
  }

  Shell_Printf("received '%s': %u bytes, crc32 0x%08X\r\n",
               info.name, (unsigned int)info.size, (unsigned int)info.crc);

  /* Verify what is really in the flash, not only what was received. */
  if (Boot_VectorValidRange((const uint8_t *)BOOT_APP_ADDR, info.size,
                            execBase, execSize) == 0)
  {
    Shell_Printf("rejected: no valid vector table at 0x%08X for 0x%08X..0x%08X\r\n",
                 (unsigned int)BOOT_APP_ADDR,
                 (unsigned int)execBase,
                 (unsigned int)(execBase + execSize - 1U));
    return 0;
  }

  if (Boot_ImageCrc(BOOT_APP_ADDR, info.size) != info.crc)
  {
    Shell_Printf("rejected: flash read back does not match the received data\r\n");
    return 0;
  }

  if (isBootloader != 0)
  {
    Shell_Printf("verified in the slot. the copy cannot be undone: a power cut\r\n"
                 "leaves the device usable only through SWD.\r\n"
                 "send any key within 5 s to cancel, or wait to continue\r\n");

    for (seconds = 0U; seconds < 5U; seconds++)
    {
      uint32_t elapsed = 0U;
      uint8_t peek;

      Shell_WriteRaw((const uint8_t *)".", 1U);

      while (elapsed < 1000U)
      {
        if (Shell_PeekByte(&peek) != 0)
        {
          Shell_Flush();
          Shell_Printf("\r\nupdate cancelled, nothing changed.\r\n");
          return 0;
        }
        HAL_Delay(10U);
        elapsed += 10U;
      }
    }

    Shell_Printf("\r\ncopying, the device will reset ...\r\n");
    HAL_Delay(20U);

    result = Boot_ApplyBootloaderUpdate(info.size, info.crc);

    Shell_Printf("the copy did not start (%d), nothing was changed\r\n", result);
    return 0;
  }

  /* Only now, with the image verified in place, the record is committed. */
  if (hasFlag != 0)
  {
    flag.seq++;
  }
  else
  {
    memset(&flag, 0, sizeof(flag));
    flag.seq = 1U;
  }

  flag.state = BOOT_STATE_IDLE;
  flag.slot = 0U;
  flag.image_size = info.size;
  flag.image_crc = info.crc;

  if (Boot_WriteFlag(&flag) != BOOT_OK)
  {
    Shell_Printf("warning: no flag written, only the vector table is checked\r\n");
    return 0;
  }

  Shell_Printf("download verified and recorded: %u bytes, crc32 0x%08X\r\n",
               (unsigned int)info.size, (unsigned int)info.crc);
  Shell_Printf("'run latest' starts it now, a reset starts it after the countdown.\r\n");

  /* The sender leaves a few bytes (padding, trailing 0xFF) in the ring; drop
     them so the console does not try to run them as a command. */
  HAL_Delay(50U);
  Shell_Flush();

  return 0;
}

static int shellCmd_run(int argc, char *argv[])
{
  BootFlag flag;
  int hasFlag;

  if (argc != 2)
  {
    Shell_Printf("usage: run latest | run bukup\r\n");
    return 0;
  }

  if ((shellArgIs(argv[1], "latest") == 0) &&
      (shellArgIs(argv[1], "bukup") == 0) &&
      (shellArgIs(argv[1], "backup") == 0))
  {
    Shell_Printf("usage: run latest | run bukup\r\n");
    return 0;
  }

  if ((shellArgIs(argv[1], "bukup") != 0) || (shellArgIs(argv[1], "backup") != 0))
  {
    Shell_Printf("one slot only (64 KB part): there is no backup image;\r\n"
                 "'run latest' starts the current one.\r\n");
    return 0;
  }

  if (Boot_ImageValid(BOOT_APP_ADDR) == 0)
  {
    Shell_Printf("no valid application; send one with 'ymodem-ota k'.\r\n");
    return 0;
  }

  hasFlag = Boot_ReadFlag(&flag);
  if (hasFlag == 0)
  {
    Shell_Printf("warning: no flag, only the vector table is checked\r\n");
  }
  else if (Boot_ImageCrc(BOOT_APP_ADDR, flag.image_size) != flag.image_crc)
  {
    Shell_Printf("the image does not match the recorded crc, not starting it.\r\n");
    return 0;
  }

  Shell_Printf("starting application at 0x%08X ...\r\n",
               (unsigned int)BOOT_APP_ADDR);
  HAL_Delay(20U);
  Boot_JumpToApp();

  /* Only reached when the jump was refused. */
  return 0;
}

static int shellCmd_bootinfo(int argc, char *argv[])
{
  BootFlag flag;
  uint32_t flashKb = shellFlashSizeKb();
  int hasFlag;

  (void)argc;
  (void)argv;

  Shell_Printf("flash %u KB / need %u KB\r\n",
               (unsigned int)flashKb, (unsigned int)Boot_RequiredFlashKb());
  if (flashKb < Boot_RequiredFlashKb())
  {
    Shell_Printf("warning: this device is too small for the layout\r\n");
  }

  Shell_Printf("boot 0x%08X-0x%08X\r\n",
               (unsigned int)BOOT_FLASH_BASE,
               (unsigned int)(BOOT_FLAG_ADDR - 1U));
  Shell_Printf("flag 0x%08X-0x%08X\r\n",
               (unsigned int)BOOT_FLAG_ADDR,
               (unsigned int)(BOOT_APP_ADDR - 1U));
  Shell_Printf("app  0x%08X-0x%08X\r\n",
               (unsigned int)BOOT_APP_ADDR,
               (unsigned int)(BOOT_APP_ADDR + BOOT_APP_SIZE - 1U));

  hasFlag = Boot_ReadFlag(&flag);
  if (hasFlag == 0)
  {
    Shell_Printf("flag: none\r\n");
  }
  else
  {
    Shell_Printf("flag: seq %u %s %u bytes crc 0x%08X\r\n",
                 (unsigned int)flag.seq,
                 shellBootStateName(flag.state),
                 (unsigned int)flag.image_size,
                 (unsigned int)flag.image_crc);
  }

  if (Boot_ImageValid(BOOT_APP_ADDR) != 0)
  {
    Shell_Printf("vector: sp 0x%08X reset 0x%08X\r\n",
                 (unsigned int)(*(__IO uint32_t *)BOOT_APP_ADDR),
                 (unsigned int)(*(__IO uint32_t *)(BOOT_APP_ADDR + 4U)));
  }
  else
  {
    Shell_Printf("vector: no valid image in the slot\r\n");
    return 0;
  }

  if (hasFlag == 0)
  {
    Shell_Printf("crc: no record\r\n");
  }
  else if (Boot_ImageCrc(BOOT_APP_ADDR, flag.image_size) == flag.image_crc)
  {
    Shell_Printf("crc: matches the record\r\n");
  }
  else
  {
    Shell_Printf("crc: MISMATCH, the image is incomplete\r\n");
  }

  return 0;
}
