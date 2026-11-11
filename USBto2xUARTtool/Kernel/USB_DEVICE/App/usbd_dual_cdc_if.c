/**
  ******************************************************************************
  * @file    usbd_dual_cdc_if.c
  * @brief   Application interface of the triple CDC class (three VCPs).
  *
  * Port 0 -> UART2 bridge, port 1 -> UART3 bridge, port 2 -> letter-shell
  * command console.
  ******************************************************************************
  */

/* Includes ------------------------------------------------------------------*/
#include "usbd_dual_cdc_if.h"
#include "usb_device.h"
#include "bridge.h"
#include "shell_port.h"

/* Private defines -----------------------------------------------------------*/
#define APP_RX_DATA_SIZE  64U
#define APP_TX_DATA_SIZE  64U

/* Private variables ---------------------------------------------------------*/
static uint8_t UserRxBufferFS[DUALCDC_PORT_COUNT][APP_RX_DATA_SIZE];
static uint8_t UserTxBufferFS[DUALCDC_PORT_COUNT][APP_TX_DATA_SIZE];

/* Line coding reported for the shell port: the console has no real UART, so
   the value is stored only to satisfy the host driver. */
static uint8_t ShellLineCoding[7] = {0x00U, 0xC2U, 0x01U, 0x00U, 0x00U, 0x00U, 0x08U};

/* Private function prototypes -----------------------------------------------*/
static int8_t DualCDC_Init(uint8_t port);
static int8_t DualCDC_DeInit(uint8_t port);
static int8_t DualCDC_Control(uint8_t port, uint8_t cmd, uint8_t *pbuf, uint16_t len);
static int8_t DualCDC_Receive(uint8_t port, uint8_t *Buf, uint32_t *Len);

/* Application interface table */
USBD_DUALCDC_ItfTypeDef USBD_Interface_fops_FS =
{
  DualCDC_Init,
  DualCDC_DeInit,
  DualCDC_Control,
  DualCDC_Receive
};

/* Private functions ---------------------------------------------------------*/

/**
  * @brief  Class initialisation: hand the buffers of a port to the class.
  */
static int8_t DualCDC_Init(uint8_t port)
{
  if (port >= DUALCDC_PORT_COUNT)
  {
    return (int8_t)USBD_FAIL;
  }

  (void)USBD_DUALCDC_SetTxBuffer(&hUsbDeviceFS, UserTxBufferFS[port],
                                 APP_TX_DATA_SIZE, port);
  (void)USBD_DUALCDC_SetRxBuffer(&hUsbDeviceFS, UserRxBufferFS[port], port);

  /* Make sure the port starts from a clean state. */
  if (port < DUALCDC_SHELL_PORT)
  {
    Bridge_Reset(port);
  }

  return (int8_t)USBD_OK;
}

/**
  * @brief  Class de-initialisation.
  */
static int8_t DualCDC_DeInit(uint8_t port)
{
  if (port >= DUALCDC_PORT_COUNT)
  {
    return (int8_t)USBD_FAIL;
  }

  if (port < DUALCDC_SHELL_PORT)
  {
    Bridge_Reset(port);
  }

  return (int8_t)USBD_OK;
}

/**
  * @brief  Class specific control requests.
  */
static int8_t DualCDC_Control(uint8_t port, uint8_t cmd, uint8_t *pbuf, uint16_t len)
{
  USBD_SetupReqTypedef *req = (USBD_SetupReqTypedef *)pbuf;

  if (port >= DUALCDC_PORT_COUNT)
  {
    return (int8_t)USBD_FAIL;
  }

  if (port < DUALCDC_SHELL_PORT)
  {
    /* Virtual COM ports bridged to a real UART. */
    switch (cmd)
    {
      case DUALCDC_REQ_SET_LINE_CODING:
        Bridge_SetLineCoding(port, pbuf);
        break;

      case DUALCDC_REQ_GET_LINE_CODING:
        Bridge_GetLineCoding(port, pbuf);
        break;

      case DUALCDC_REQ_SET_CONTROL_LINE_STATE:
        Bridge_SetControlLineState(port, (uint8_t)(req->wValue & 0x03U));
        break;

      case DUALCDC_REQ_SEND_BREAK:
        /* Not supported by the bridge. */
        break;

      default:
        return (int8_t)USBD_FAIL;
    }
    return (int8_t)USBD_OK;
  }

  /* letter-shell console: there is no UART behind it, so only the line coding
     is answered for compatibility with the host driver. */
  switch (cmd)
  {
    case DUALCDC_REQ_SET_LINE_CODING:
      if (len >= sizeof(ShellLineCoding))
      {
        for (uint8_t i = 0U; i < sizeof(ShellLineCoding); i++)
        {
          ShellLineCoding[i] = pbuf[i];
        }
      }
      break;

    case DUALCDC_REQ_GET_LINE_CODING:
      for (uint8_t i = 0U; i < sizeof(ShellLineCoding); i++)
      {
        pbuf[i] = ShellLineCoding[i];
      }
      break;

    case DUALCDC_REQ_SET_CONTROL_LINE_STATE:
    case DUALCDC_REQ_SEND_BREAK:
      break;

    default:
      return (int8_t)USBD_FAIL;
  }

  return (int8_t)USBD_OK;
}

/**
  * @brief  Data received from the host on one port.
  */
static int8_t DualCDC_Receive(uint8_t port, uint8_t *Buf, uint32_t *Len)
{
  if (port >= DUALCDC_PORT_COUNT)
  {
    return (int8_t)USBD_FAIL;
  }

  if (port < DUALCDC_SHELL_PORT)
  {
    Bridge_UsbToUart(port, Buf, *Len);
  }
  else
  {
    Shell_RxBlock(Buf, *Len);
  }

  /* The buffers have been copied, so tell the class there is no pending
     application data. */
  *Len = 0U;
  (void)USBD_DUALCDC_ReceivePacket(&hUsbDeviceFS, port);

  return (int8_t)USBD_OK;
}

/* Public functions ----------------------------------------------------------*/

uint8_t DualCDC_Transmit(uint8_t port, uint8_t *buf, uint16_t len)
{
  if ((port >= DUALCDC_PORT_COUNT) || (len > APP_TX_DATA_SIZE))
  {
    return (uint8_t)USBD_FAIL;
  }

  if (hUsbDeviceFS.dev_state != USBD_STATE_CONFIGURED)
  {
    return (uint8_t)USBD_FAIL;
  }

  return USBD_DUALCDC_Transmit_FS(&hUsbDeviceFS, buf, len, port);
}

uint8_t DualCDC_TxReady(uint8_t port)
{
  if (port >= DUALCDC_PORT_COUNT)
  {
    return 0U;
  }

  if (hUsbDeviceFS.dev_state != USBD_STATE_CONFIGURED)
  {
    return 0U;
  }

  return USBD_DUALCDC_TxReady(&hUsbDeviceFS, port);
}

uint8_t DualCDC_IsConfigured(void)
{
  return (hUsbDeviceFS.dev_state == USBD_STATE_CONFIGURED) ? 1U : 0U;
}
