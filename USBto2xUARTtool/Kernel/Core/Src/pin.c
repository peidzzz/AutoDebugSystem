/**
  ******************************************************************************
  * @file    pin.c
  * @brief   GPIO shell command.
  *
  *   pin <pin> in           configure a pin as input (floating)
  *   pin <pin> out          configure a pin as push-pull output
  *   pin read <pin>         read the level of a pin
  *   pin write <pin> <0|1>  drive an output pin (output mode only)
  *
  * <pin> is a port letter followed by the pin number, e.g. pb5, pa0, pc13.
  * The current mode of a pin is read back from the GPIO configuration
  * registers, so pins configured elsewhere in the firmware are handled too.
  ******************************************************************************
  */

/* Includes ------------------------------------------------------------------*/
#include "pin.h"
#include "main.h"
#include "shell.h"

#include <stdint.h>
#include <stdlib.h>
#include <string.h>

/* Private defines -----------------------------------------------------------*/
#define PIN_PORT_COUNT   5U
#define PIN_PER_PORT     16U
#define PIN_NAME_SIZE    8U

/* Private types -------------------------------------------------------------*/
typedef enum
{
  PIN_DIR_INPUT = 0,
  PIN_DIR_OUTPUT
} PinDirection;

/* Private function prototypes -----------------------------------------------*/
static int pinCmd_Usage(Shell *sh);
static int pinCmd_Configure(Shell *sh, const char *name, const char *mode, int force);
static int pinCmd_Read(Shell *sh, const char *name);
static int pinCmd_Write(Shell *sh, const char *name, const char *value);
static int pinParse(const char *text, uint8_t *portIndex, uint8_t *number);
static GPIO_TypeDef *pinPort(uint8_t portIndex);
static void pinEnableClock(uint8_t portIndex);
static PinDirection pinDirection(uint8_t portIndex, uint8_t number);
static void pinWarnIfDebugPin(uint8_t portIndex, uint8_t number);

/* Exported commands ---------------------------------------------------------*/

SHELL_EXPORT_CMD(SHELL_CMD_PERMISSION(0) | SHELL_CMD_TYPE(SHELL_TYPE_CMD_MAIN) |
                 SHELL_CMD_DISABLE_RETURN,
                 pin, Pin_ShellCmd, control GPIO: in/out/read/write);

/* Private functions ---------------------------------------------------------*/

static GPIO_TypeDef *pinPort(uint8_t portIndex)
{
  switch (portIndex)
  {
    case 0U: return GPIOA;
    case 1U: return GPIOB;
    case 2U: return GPIOC;
    case 3U: return GPIOD;
    case 4U: return GPIOE;
    default: return NULL;
  }
}

static void pinEnableClock(uint8_t portIndex)
{
  switch (portIndex)
  {
    case 0U: __HAL_RCC_GPIOA_CLK_ENABLE(); break;
    case 1U: __HAL_RCC_GPIOB_CLK_ENABLE(); break;
    case 2U: __HAL_RCC_GPIOC_CLK_ENABLE(); break;
    case 3U: __HAL_RCC_GPIOD_CLK_ENABLE(); break;
    case 4U: __HAL_RCC_GPIOE_CLK_ENABLE(); break;
    default: break;
  }
}

/**
  * @brief  Parse a pin name such as "pb5", "P A 0" (no spaces) or "c13".
  * @param  portIndex receives 0..4 for port A..E
  * @param  number    receives 0..15
  * @retval 0 on success, -1 on a malformed name
  */
static int pinParse(const char *text, uint8_t *portIndex, uint8_t *number)
{
  const char *p = text;
  char portChar;
  char *end = NULL;
  long value;

  if ((text == NULL) || (portIndex == NULL) || (number == NULL))
  {
    return -1;
  }

  /* The "p" prefix is optional: "pb5" and "b5" are both accepted. */
  if ((*p == 'p') || (*p == 'P'))
  {
    p++;
  }

  portChar = *p;
  p++;

  if ((portChar >= 'a') && (portChar <= 'e'))
  {
    *portIndex = (uint8_t)(portChar - 'a');
  }
  else if ((portChar >= 'A') && (portChar <= 'E'))
  {
    *portIndex = (uint8_t)(portChar - 'A');
  }
  else
  {
    return -1;
  }

  if ((*p < '0') || (*p > '9'))
  {
    return -1;
  }

  value = strtol(p, &end, 10);
  if ((end == p) || (*end != '\0'))
  {
    return -1;
  }
  if ((value < 0) || (value >= (long)PIN_PER_PORT))
  {
    return -1;
  }

  *number = (uint8_t)value;
  return 0;
}

/**
  * @brief  Read back the direction of a pin from CRL/CRH (MODE[1:0] bits).
  */
static PinDirection pinDirection(uint8_t portIndex, uint8_t number)
{
  GPIO_TypeDef *port = pinPort(portIndex);
  uint32_t config;
  uint32_t modeBits;

  if (port == NULL)
  {
    return PIN_DIR_INPUT;
  }

  if (number < 8U)
  {
    config = port->CRL;
    modeBits = (config >> (number * 4U)) & 0x03U;
  }
  else
  {
    config = port->CRH;
    modeBits = (config >> ((number - 8U) * 4U)) & 0x03U;
  }

  return (modeBits == 0U) ? PIN_DIR_INPUT : PIN_DIR_OUTPUT;
}

/**
  * @brief  Warn when a pin belongs to the JTAG-only part of the debug port.
  * @note   PA13/PA14 (SWDIO/SWCLK) are refused by pinCmd_Configure() instead,
  *         because reconfiguring them detaches the DAP/SWD debugger for good.
  */
static void pinWarnIfDebugPin(uint8_t portIndex, uint8_t number)
{
  Shell *sh = shellGetCurrent();

  if ((portIndex == 0U) && (number == 15U))
  {
    shellPrint(sh, "warning: p%c%u is used by JTAG, debugging may stop working\r\n",
               (char)('a' + portIndex), (unsigned)number);
  }
  else if ((portIndex == 1U) && ((number == 3U) || (number == 4U)))
  {
    shellPrint(sh, "warning: p%c%u is used by JTAG, debugging may stop working\r\n",
               (char)('a' + portIndex), (unsigned)number);
  }
  else
  {
    /* nothing to warn about */
  }
}

/**
  * @brief  True for the two pins of the SWD link the DAP downloader uses.
  */
static int pinIsSwdPin(uint8_t portIndex, uint8_t number)
{
  return ((portIndex == 0U) && ((number == 13U) || (number == 14U))) ? 1 : 0;
}


static int pinCmd_Usage(Shell *sh)
{
  shellPrint(sh, "usage:\r\n");
  shellPrint(sh, "  pin <pin> in           configure <pin> as input\r\n");
  shellPrint(sh, "  pin <pin> out          configure <pin> as output\r\n");
  shellPrint(sh, "  pin read <pin>         read the level of <pin>\r\n");
  shellPrint(sh, "  pin write <pin> <0|1>  drive an output pin\r\n");
  shellPrint(sh, "  pin <pin> <in|out> force  allow pa13/pa14 (kills SWD)\r\n");
  shellPrint(sh, "  <pin> = port letter + number, e.g. pa0, pb5, pc13\r\n");
  return 0;
}

static int pinCmd_Configure(Shell *sh, const char *name, const char *mode, int force)
{
  GPIO_InitTypeDef init;
  uint8_t portIndex = 0U;
  uint8_t number = 0U;

  if (pinParse(name, &portIndex, &number) != 0)
  {
    shellPrint(sh, "error: invalid pin '%s' (use e.g. pb5)\r\n", name);
    return 0;
  }

  if ((pinIsSwdPin(portIndex, number) != 0) && (force == 0))
  {
    shellPrint(sh, "error: p%c%u is the SWD link, refused (add 'force' to override)\r\n",
               (char)('a' + portIndex), (unsigned)number);
    return 0;
  }

  pinEnableClock(portIndex);

  memset(&init, 0, sizeof(init));
  init.Pin = (uint16_t)(1U << number);
  init.Speed = GPIO_SPEED_FREQ_LOW;
  init.Pull = GPIO_NOPULL;

  if ((mode[0] == 'i') || (mode[0] == 'I'))
  {
    init.Mode = GPIO_MODE_INPUT;
    HAL_GPIO_Init(pinPort(portIndex), &init);
    shellPrint(sh, "p%c%u -> input\r\n", (char)('a' + portIndex), (unsigned)number);
  }
  else if ((mode[0] == 'o') || (mode[0] == 'O'))
  {
    init.Mode = GPIO_MODE_OUTPUT_PP;
    HAL_GPIO_Init(pinPort(portIndex), &init);
    shellPrint(sh, "p%c%u -> output\r\n", (char)('a' + portIndex), (unsigned)number);
  }
  else
  {
    shellPrint(sh, "error: mode must be 'in' or 'out'\r\n");
    return 0;
  }

  pinWarnIfDebugPin(portIndex, number);
  return 0;
}

static int pinCmd_Read(Shell *sh, const char *name)
{
  uint8_t portIndex = 0U;
  uint8_t number = 0U;
  GPIO_PinState level;

  if (pinParse(name, &portIndex, &number) != 0)
  {
    shellPrint(sh, "error: invalid pin '%s' (use e.g. pb5)\r\n", name);
    return 0;
  }

  pinEnableClock(portIndex);
  level = HAL_GPIO_ReadPin(pinPort(portIndex), (uint16_t)(1U << number));

  shellPrint(sh, "p%c%u = %u (%s, %s)\r\n",
             (char)('a' + portIndex), (unsigned)number,
             (unsigned)level,
             (level == GPIO_PIN_SET) ? "high" : "low",
             (pinDirection(portIndex, number) == PIN_DIR_OUTPUT) ? "output" : "input");
  return 0;
}

static int pinCmd_Write(Shell *sh, const char *name, const char *value)
{
  uint8_t portIndex = 0U;
  uint8_t number = 0U;
  long level;
  char *end = NULL;

  if (pinParse(name, &portIndex, &number) != 0)
  {
    shellPrint(sh, "error: invalid pin '%s' (use e.g. pb5)\r\n", name);
    return 0;
  }

  level = strtol(value, &end, 10);
  if ((end == value) || (*end != '\0') || ((level != 0) && (level != 1)))
  {
    shellPrint(sh, "error: value must be 0 or 1\r\n");
    return 0;
  }

  pinEnableClock(portIndex);

  if (pinDirection(portIndex, number) != PIN_DIR_OUTPUT)
  {
    shellPrint(sh, "error: p%c%u is an input, run 'pin p%c%u out' first\r\n",
               (char)('a' + portIndex), (unsigned)number,
               (char)('a' + portIndex), (unsigned)number);
    return 0;
  }

  HAL_GPIO_WritePin(pinPort(portIndex), (uint16_t)(1U << number),
                    (level != 0) ? GPIO_PIN_SET : GPIO_PIN_RESET);
  shellPrint(sh, "p%c%u = %ld\r\n", (char)('a' + portIndex), (unsigned)number, level);
  return 0;
}

/* Public functions ----------------------------------------------------------*/

int Pin_ShellCmd(int argc, char *argv[])
{
  Shell *sh = shellGetCurrent();

  if (argc < 2)
  {
    return pinCmd_Usage(sh);
  }

  if (strcmp(argv[1], "read") == 0)
  {
    if (argc != 3)
    {
      shellPrint(sh, "error: usage: pin read <pin>\r\n");
      return 0;
    }
    return pinCmd_Read(sh, argv[2]);
  }

  if (strcmp(argv[1], "write") == 0)
  {
    if (argc != 4)
    {
      shellPrint(sh, "error: usage: pin write <pin> <0|1>\r\n");
      return 0;
    }
    return pinCmd_Write(sh, argv[2], argv[3]);
  }

  if (argc == 3)
  {
    return pinCmd_Configure(sh, argv[1], argv[2], 0);
  }

  if ((argc == 4) && (strcmp(argv[3], "force") == 0))
  {
    return pinCmd_Configure(sh, argv[1], argv[2], 1);
  }

  return pinCmd_Usage(sh);
}
