/**
  ******************************************************************************
  * @file    bridge.c
  * @brief   USB <-> UART2 / UART3 bridge implementation.
  *
  * Data path:
  *   USB OUT  -> ring buffer (usbToUart) -> UART TX   (blocking, task context)
  *   UART RX  -> ring buffer (uartToUsb) -> USB IN    (packet, task context)
  *
  * The UART receive interrupts only push bytes into a lock-free single
  * producer / single consumer ring, the tasks do the actual transfers.
  ******************************************************************************
  */

/* Includes ------------------------------------------------------------------*/
#include "bridge.h"
#include "main.h"
#include "usart.h"
#include "usbd_dual_cdc_if.h"

#include "FreeRTOS.h"
#include "task.h"

#include <string.h>

/* Private defines -----------------------------------------------------------*/
#define BRIDGE_RING_SIZE        256U
#define BRIDGE_RING_MASK        (BRIDGE_RING_SIZE - 1U)
#define BRIDGE_TASK_STACK       256U
#define BRIDGE_TASK_PRIORITY    (tskIDLE_PRIORITY + 2U)
#define BRIDGE_TX_CHUNK         64U

/* Private types -------------------------------------------------------------*/
typedef struct
{
  uint8_t buf[BRIDGE_RING_SIZE];
  volatile uint16_t head;   /* written by producer only */
  volatile uint16_t tail;   /* written by consumer only */
} BridgeRing;

typedef struct
{
  BridgeRing usbToUart;
  BridgeRing uartToUsb;
  uint8_t lineCoding[7];
  volatile uint8_t lineCodingPending;
  volatile uint8_t controlLineState;
  UART_HandleTypeDef *huart;
  uint8_t rxByte;
} BridgePort;

/* Private variables ---------------------------------------------------------*/
static BridgePort bridgePort[2];
static TaskHandle_t bridgeTaskHandle[2];

/* Private function prototypes -----------------------------------------------*/
static void Bridge_Task(void *argument);
static void Bridge_ApplyLineCoding(uint8_t port);
static void RingPush(BridgeRing *r, uint8_t byte);
static uint16_t RingPeek(BridgeRing *r, uint8_t *out, uint16_t max);
static void RingConsume(BridgeRing *r, uint16_t count);

/* Private functions ---------------------------------------------------------*/

static void RingPush(BridgeRing *r, uint8_t byte)
{
  uint16_t next = (uint16_t)((r->head + 1U) & BRIDGE_RING_MASK);

  if (next != r->tail)
  {
    r->buf[r->head] = byte;
    r->head = next;
  }
  /* else: ring full, byte is dropped */
}

static uint16_t RingPeek(BridgeRing *r, uint8_t *out, uint16_t max)
{
  uint16_t n = 0U;
  uint16_t t = r->tail;

  while ((n < max) && (t != r->head))
  {
    out[n++] = r->buf[t];
    t = (uint16_t)((t + 1U) & BRIDGE_RING_MASK);
  }
  return n;
}

static void RingConsume(BridgeRing *r, uint16_t count)
{
  r->tail = (uint16_t)((r->tail + count) & BRIDGE_RING_MASK);
}

static uint32_t Bridge_GetCodingBaudRate(const uint8_t *coding)
{
  return ((uint32_t)coding[0]) |
         ((uint32_t)coding[1] << 8) |
         ((uint32_t)coding[2] << 16) |
         ((uint32_t)coding[3] << 24);
}

static void Bridge_SetCodingBaudRate(uint8_t *coding, uint32_t baud)
{
  coding[0] = (uint8_t)(baud & 0xFFU);
  coding[1] = (uint8_t)((baud >> 8) & 0xFFU);
  coding[2] = (uint8_t)((baud >> 16) & 0xFFU);
  coding[3] = (uint8_t)((baud >> 24) & 0xFFU);
}

static void Bridge_ApplyLineCoding(uint8_t port)
{
  BridgePort *p = &bridgePort[port & 0x01U];
  UART_HandleTypeDef *huart = p->huart;
  uint32_t baud = Bridge_GetCodingBaudRate(p->lineCoding);
  uint8_t stopBits = p->lineCoding[4];
  uint8_t parity = p->lineCoding[5];
  uint8_t dataBits = p->lineCoding[6];

  if ((baud < 50U) || (baud > 4500000U))
  {
    baud = 115200U;
  }

  HAL_UART_DeInit(huart);

  huart->Init.BaudRate = baud;
  huart->Init.WordLength = (dataBits == 9U) ? UART_WORDLENGTH_9B : UART_WORDLENGTH_8B;

  switch (stopBits)
  {
    /* STM32F1 UARTs only support 1 or 2 stop bits. */
    case 2U:  huart->Init.StopBits = UART_STOPBITS_2;   break;
    default:  huart->Init.StopBits = UART_STOPBITS_1;   break;
  }

  switch (parity)
  {
    case 1U:  huart->Init.Parity = UART_PARITY_ODD;  break;
    case 2U:  huart->Init.Parity = UART_PARITY_EVEN; break;
    default:  huart->Init.Parity = UART_PARITY_NONE; break;
  }

  huart->Init.Mode = UART_MODE_TX_RX;
  huart->Init.HwFlowCtl = UART_HWCONTROL_NONE;
  huart->Init.OverSampling = UART_OVERSAMPLING_16;

  (void)HAL_UART_Init(huart);

  /* Restart interrupt driven reception on the reconfigured UART. */
  (void)HAL_UART_Receive_IT(huart, &p->rxByte, 1U);

  /* Keep the 7 byte line coding coherent with what was really programmed. */
  Bridge_SetCodingBaudRate(p->lineCoding, baud);
}

static void Bridge_Task(void *argument)
{
  uint8_t port = (uint8_t)(uintptr_t)argument;
  BridgePort *p = &bridgePort[port & 0x01U];
  uint8_t buffer[BRIDGE_TX_CHUNK];
  uint16_t count;
  uint8_t worked;

  for (;;)
  {
    worked = 0U;

    if (p->lineCodingPending != 0U)
    {
      p->lineCodingPending = 0U;
      Bridge_ApplyLineCoding(port);
      worked = 1U;
    }

    /* UART -> USB.  The USB peripheral reads from "buffer" asynchronously, so
       it must only be refilled once the previous transfer has completed. */
    if (DualCDC_TxReady(port) != 0U)
    {
      count = RingPeek(&p->uartToUsb, buffer, (uint16_t)sizeof(buffer));
      if ((count > 0U) && (DualCDC_Transmit(port, buffer, count) == USBD_OK))
      {
        RingConsume(&p->uartToUsb, count);
        worked = 1U;
      }
    }

    /* USB -> UART */
    count = RingPeek(&p->usbToUart, buffer, (uint16_t)sizeof(buffer));
    if (count > 0U)
    {
      (void)HAL_UART_Transmit(p->huart, buffer, count, HAL_MAX_DELAY);
      RingConsume(&p->usbToUart, count);
      worked = 1U;
    }

    /* Only sleep when there was nothing to do, so an active stream is not
       throttled to one 64 byte packet per millisecond. */
    if (worked == 0U)
    {
      vTaskDelay(pdMS_TO_TICKS(1));
    }
  }
}

/* Public functions ----------------------------------------------------------*/

void Bridge_Init(void)
{
  BaseType_t ret;

  bridgePort[0].huart = &huart2;
  bridgePort[1].huart = &huart3;

  for (uint8_t i = 0U; i < 2U; i++)
  {
    memset(&bridgePort[i].usbToUart, 0, sizeof(BridgeRing));
    memset(&bridgePort[i].uartToUsb, 0, sizeof(BridgeRing));
    memset(bridgePort[i].lineCoding, 0, sizeof(bridgePort[i].lineCoding));

    /* Default: 115200 baud, 1 stop bit, no parity, 8 data bits.
       baud rate is stored little endian as in the CDC line coding. */
    bridgePort[i].lineCoding[0] = 0x00U;   /* 115200 = 0x0001C200 */
    bridgePort[i].lineCoding[1] = 0xC2U;
    bridgePort[i].lineCoding[2] = 0x01U;
    bridgePort[i].lineCoding[3] = 0x00U;
    bridgePort[i].lineCoding[4] = 0x00U;   /* 1 stop bit */
    bridgePort[i].lineCoding[5] = 0x00U;   /* no parity */
    bridgePort[i].lineCoding[6] = 0x08U;   /* 8 data bits */

    bridgePort[i].lineCodingPending = 0U;
    bridgePort[i].controlLineState = 0U;
    bridgePort[i].rxByte = 0U;
  }

  /* Bridge UARTs: one byte interrupt reception.  UART1 is no longer used: the
     letter-shell console lives on the third virtual COM port. */
  (void)HAL_UART_Receive_IT(&huart2, &bridgePort[0].rxByte, 1U);
  (void)HAL_UART_Receive_IT(&huart3, &bridgePort[1].rxByte, 1U);

  ret = xTaskCreate(Bridge_Task, "bridge0", BRIDGE_TASK_STACK,
                    (void *)(uintptr_t)0U, BRIDGE_TASK_PRIORITY,
                    &bridgeTaskHandle[0]);
  configASSERT(ret == pdPASS);

  ret = xTaskCreate(Bridge_Task, "bridge1", BRIDGE_TASK_STACK,
                    (void *)(uintptr_t)1U, BRIDGE_TASK_PRIORITY,
                    &bridgeTaskHandle[1]);
  configASSERT(ret == pdPASS);
}

void Bridge_UsbToUart(uint8_t port, const uint8_t *data, uint32_t len)
{
  BridgePort *p = &bridgePort[port & 0x01U];

  for (uint32_t i = 0U; i < len; i++)
  {
    RingPush(&p->usbToUart, data[i]);
  }
}

void Bridge_SetLineCoding(uint8_t port, const uint8_t *coding)
{
  BridgePort *p = &bridgePort[port & 0x01U];

  memcpy(p->lineCoding, coding, sizeof(p->lineCoding));
  p->lineCodingPending = 1U;
}

void Bridge_GetLineCoding(uint8_t port, uint8_t *coding)
{
  BridgePort *p = &bridgePort[port & 0x01U];

  memcpy(coding, p->lineCoding, sizeof(p->lineCoding));
}

void Bridge_SetControlLineState(uint8_t port, uint8_t state)
{
  bridgePort[port & 0x01U].controlLineState = state;
}

void Bridge_Reset(uint8_t port)
{
  BridgePort *p = &bridgePort[port & 0x01U];

  p->usbToUart.head = 0U;
  p->usbToUart.tail = 0U;
  p->uartToUsb.head = 0U;
  p->uartToUsb.tail = 0U;
  p->lineCodingPending = 0U;
  p->controlLineState = 0U;
}

uint32_t Bridge_GetBaudRate(uint8_t port)
{
  return Bridge_GetCodingBaudRate(bridgePort[port & 0x01U].lineCoding);
}

/* HAL callbacks -------------------------------------------------------------*/

void HAL_UART_RxCpltCallback(UART_HandleTypeDef *huart)
{
  if (huart->Instance == USART2)
  {
    RingPush(&bridgePort[0].uartToUsb, bridgePort[0].rxByte);
    (void)HAL_UART_Receive_IT(huart, &bridgePort[0].rxByte, 1U);
  }
  else if (huart->Instance == USART3)
  {
    RingPush(&bridgePort[1].uartToUsb, bridgePort[1].rxByte);
    (void)HAL_UART_Receive_IT(huart, &bridgePort[1].rxByte, 1U);
  }
  else
  {
    /* nothing to do */
  }
}

void HAL_UART_ErrorCallback(UART_HandleTypeDef *huart)
{
  /* A framing/overrun error aborts reception: restart it so the bridge keeps
     working. */
  if (huart->Instance == USART2)
  {
    (void)HAL_UART_Receive_IT(huart, &bridgePort[0].rxByte, 1U);
  }
  else if (huart->Instance == USART3)
  {
    (void)HAL_UART_Receive_IT(huart, &bridgePort[1].rxByte, 1U);
  }
  else
  {
    /* nothing to do */
  }
}
