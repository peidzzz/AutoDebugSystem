/**
  ******************************************************************************
  * @file    shell_port.c
  * @brief   letter-shell port: console on the third virtual COM port
  *          (USB CDC port 2), FreeRTOS task, LED command.
  ******************************************************************************
  */

/* Includes ------------------------------------------------------------------*/
#include "shell_port.h"
#include "main.h"
#include "led.h"
#include "usbd_dual_cdc.h"
#include "usbd_dual_cdc_if.h"

#include "FreeRTOS.h"
#include "task.h"
#include "semphr.h"

#include "shell.h"

#include <stdlib.h>
#include <string.h>

/* Private defines -----------------------------------------------------------*/
#define SHELL_RX_RING_SIZE         128U
#define SHELL_RX_RING_MASK         (SHELL_RX_RING_SIZE - 1U)
#define SHELL_BUFFER_SIZE          512U
#define SHELL_TASK_STACK_WORDS     384U
#define SHELL_TASK_PRIORITY        (tskIDLE_PRIORITY + 3U)
/* The USB bulk endpoint of the console port carries 16 byte packets; 64 bytes
   per transfer keeps the number of packets per shell print low. */
#define SHELL_TX_CHUNK             64U
#define SHELL_TX_TIMEOUT_MS        100U
#define SHELL_TX_CONFIG_TIMEOUT_MS 2000U

/* Private variables ---------------------------------------------------------*/
static Shell shell;
static char shellBuffer[SHELL_BUFFER_SIZE];

static uint8_t shellRxRing[SHELL_RX_RING_SIZE];
static volatile uint16_t shellRxHead = 0U;
static volatile uint16_t shellRxTail = 0U;

static uint8_t shellTxBuffer[SHELL_TX_CHUNK];

static SemaphoreHandle_t shellMutex = NULL;
static SemaphoreHandle_t shellRxSem = NULL;

/* Private function prototypes -----------------------------------------------*/
static signed short userShellWrite(char *data, unsigned short len);
static signed short userShellRead(char *data, unsigned short len);
static int userShellLock(Shell *sh);
static int userShellUnlock(Shell *sh);
static void Shell_Task(void *argument);
static int shellCmd_led(int argc, char *argv[]);

/* Exported commands ---------------------------------------------------------*/

SHELL_EXPORT_CMD(SHELL_CMD_PERMISSION(0) | SHELL_CMD_TYPE(SHELL_TYPE_CMD_MAIN) |
                 SHELL_CMD_DISABLE_RETURN,
                 led, shellCmd_led, set LED blink frequency);

/* Private functions ---------------------------------------------------------*/

/**
  * @brief  letter-shell write callback: send console output over USB.
  *         Runs in the shell task, so it may block while the endpoint is busy.
  */
static signed short userShellWrite(char *data, unsigned short len)
{
  uint16_t sent = 0U;

  if ((data == NULL) || (len == 0U))
  {
    return 0;
  }

  while (sent < len)
  {
    uint16_t chunk = (uint16_t)(len - sent);
    uint32_t start = HAL_GetTick();
    uint32_t limit;

    if (chunk > SHELL_TX_CHUNK)
    {
      chunk = SHELL_TX_CHUNK;
    }

    /* Wait until the previous transfer has been consumed by the USB
       peripheral before the shared buffer is overwritten.  Before the host has
       configured the device the wait is longer, so the banner still arrives on
       the first "open" of the console. */
    limit = (DualCDC_IsConfigured() != 0U) ? SHELL_TX_TIMEOUT_MS
                                           : SHELL_TX_CONFIG_TIMEOUT_MS;
    while (DualCDC_TxReady(DUALCDC_SHELL_PORT) == 0U)
    {
      if ((HAL_GetTick() - start) > limit)
      {
        /* No host: drop the rest instead of stalling the firmware. */
        return (signed short)sent;
      }
      vTaskDelay(1);
    }

    for (uint16_t i = 0U; i < chunk; i++)
    {
      shellTxBuffer[i] = (uint8_t)data[sent + i];
    }

    if (DualCDC_Transmit(DUALCDC_SHELL_PORT, shellTxBuffer, chunk) != USBD_OK)
    {
      return (signed short)sent;
    }

    sent = (uint16_t)(sent + chunk);
  }

  return (signed short)len;
}

static signed short userShellRead(char *data, unsigned short len)
{
  uint16_t count = 0U;

  if ((data == NULL) || (len == 0U) || (shellRxSem == NULL))
  {
    return 0;
  }

  if (shellRxHead == shellRxTail)
  {
    /* Block until the USB interrupt queued at least one byte. */
    if (xSemaphoreTake(shellRxSem, portMAX_DELAY) != pdTRUE)
    {
      return 0;
    }
    if (shellRxHead == shellRxTail)
    {
      return 0;
    }
  }

  taskENTER_CRITICAL();
  while ((count < len) && (shellRxTail != shellRxHead))
  {
    data[count] = (char)shellRxRing[shellRxTail];
    count++;
    shellRxTail = (uint16_t)((shellRxTail + 1U) & SHELL_RX_RING_MASK);
  }
  taskEXIT_CRITICAL();

  return (signed short)count;
}

static int userShellLock(Shell *sh)
{
  (void)sh;
  if (shellMutex == NULL)
  {
    return -1;
  }
  return (xSemaphoreTakeRecursive(shellMutex, portMAX_DELAY) == pdTRUE) ? 0 : -1;
}

static int userShellUnlock(Shell *sh)
{
  (void)sh;
  if (shellMutex == NULL)
  {
    return -1;
  }
  return (xSemaphoreGiveRecursive(shellMutex) == pdTRUE) ? 0 : -1;
}

static void Shell_Task(void *argument)
{
  (void)argument;

  /* shellTask() never returns. */
  shellTask(&shell);
  vTaskDelete(NULL);
}

static int shellCmd_led(int argc, char *argv[])
{
  Shell *sh = &shell;
  long frequency;

  if (argc < 2)
  {
    if (sh != NULL)
    {
      shellPrint(sh, "LED blink frequency: %lu Hz\r\n",
                 (unsigned long)LED_GetFrequency());
      shellPrint(sh, "usage: led <0-%lu>, 0 turns the LED off\r\n",
                 (unsigned long)LED_MAX_FREQUENCY_HZ);
    }
    return 0;
  }

  frequency = strtol(argv[1], NULL, 10);
  if (frequency < 0)
  {
    frequency = 0;
  }
  if (frequency > (long)LED_MAX_FREQUENCY_HZ)
  {
    frequency = (long)LED_MAX_FREQUENCY_HZ;
  }

  LED_SetFrequency((uint32_t)frequency);

  if (sh != NULL)
  {
    if (frequency == 0)
    {
      shellPrint(sh, "LED off\r\n");
    }
    else
    {
      shellPrint(sh, "LED frequency set to %ld Hz\r\n", frequency);
    }
  }
  return 0;
}

/* Public functions ----------------------------------------------------------*/

void Shell_Init(void)
{
  BaseType_t ret;

  shellMutex = xSemaphoreCreateRecursiveMutex();
  shellRxSem = xSemaphoreCreateBinary();
  configASSERT((shellMutex != NULL) && (shellRxSem != NULL));

  /* shellInit() prints the prompt, so the callback must be set first. */
  shell.read = userShellRead;
  shell.write = userShellWrite;
  shell.lock = userShellLock;
  shell.unlock = userShellUnlock;

  shellInit(&shell, shellBuffer, SHELL_BUFFER_SIZE);

  ret = xTaskCreate(Shell_Task, "shell", SHELL_TASK_STACK_WORDS, NULL,
                    SHELL_TASK_PRIORITY, NULL);
  configASSERT(ret == pdPASS);
}

void Shell_RxBlock(const uint8_t *data, uint32_t len)
{
  BaseType_t woken = pdFALSE;
  uint16_t head;

  if ((data == NULL) || (len == 0U) || (shellRxSem == NULL))
  {
    return;
  }

  /* Single producer (USB interrupt), single consumer (shell task): the head
     index is only advanced here. */
  head = shellRxHead;
  for (uint32_t i = 0U; i < len; i++)
  {
    uint16_t next = (uint16_t)((head + 1U) & SHELL_RX_RING_MASK);

    if (next == shellRxTail)
    {
      /* Ring full: drop the remaining bytes of this block. */
      break;
    }
    if (head == shellRxTail)
    {
      (void)xSemaphoreGiveFromISR(shellRxSem, &woken);
    }
    shellRxRing[head] = data[i];
    head = next;
  }
  shellRxHead = head;

  portYIELD_FROM_ISR(woken);
}

void Shell_RxByte(uint8_t byte)
{
  Shell_RxBlock(&byte, 1U);
}
