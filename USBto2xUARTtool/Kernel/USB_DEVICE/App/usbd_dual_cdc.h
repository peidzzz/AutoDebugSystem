/**
  ******************************************************************************
  * @file    usbd_dual_cdc.h
  * @brief   Triple CDC-ACM (three virtual COM ports) composite class, full speed.
  *
  * Port 0 -> UART2 (bridge)
  * Port 1 -> UART3 (bridge)
  * Port 2 -> letter-shell console (no physical UART behind it)
  *
  * Endpoint map (the PMA of the STM32F103 is only 512 bytes wide, therefore the
  * console port uses 16 byte bulk packets so that all three ports fit):
  *   Port 0: bulk IN 0x81, bulk OUT 0x01, interrupt IN 0x82, 64 byte packets
  *   Port 1: bulk IN 0x83, bulk OUT 0x02, interrupt IN 0x84, 64 byte packets
  *   Port 2: bulk IN 0x85, bulk OUT 0x03, interrupt IN 0x86, 16 byte packets
  ******************************************************************************
  */

#ifndef __USBD_DUAL_CDC_H
#define __USBD_DUAL_CDC_H

#ifdef __cplusplus
extern "C" {
#endif

#include "usbd_ioreq.h"

/* Data class endpoints */
#define DUALCDC_CDC_IN_EP          0x81U
#define DUALCDC_CDC_OUT_EP         0x01U
#define DUALCDC_CDC_CMD_EP         0x82U

#define DUALCDC_CDC2_IN_EP         0x83U
#define DUALCDC_CDC2_OUT_EP        0x02U
#define DUALCDC_CDC2_CMD_EP        0x84U

#define DUALCDC_CDC3_IN_EP         0x85U
#define DUALCDC_CDC3_OUT_EP        0x03U
#define DUALCDC_CDC3_CMD_EP        0x86U

#define DUALCDC_DATA_FS_MAX_PACKET_SIZE   64U
/* The shell port uses 8 byte bulk packets: every bulk OUT buffer occupies at
   least one 32 byte PMA block, so 8 bytes costs one block.  A console needs
   neither bandwidth nor latency and this keeps the whole map inside the 448
   bytes that remain after the buffer descriptor table. */
#define DUALCDC_DATA_SHELL_MAX_PACKET_SIZE 8U
#define DUALCDC_CMD_PACKET_SIZE           8U

/* 9 (configuration) + 66 (function 0) + 66 (function 1) + 66 (function 2) */
#define DUALCDC_CONFIG_DESC_SIZ    207U

/* CDC class-specific requests */
#define DUALCDC_REQ_SET_LINE_CODING        0x20U
#define DUALCDC_REQ_GET_LINE_CODING        0x21U
#define DUALCDC_REQ_SET_CONTROL_LINE_STATE 0x22U
#define DUALCDC_REQ_SEND_BREAK             0x23U

/* Number of virtual ports handled by this class */
#define DUALCDC_PORT_COUNT         3U

/* Port index of the letter-shell console. */
#define DUALCDC_SHELL_PORT         2U

/** Per-port data shared between the class driver and the application. */
typedef struct
{
  uint32_t CmdOpCode;
  uint32_t CmdLength;
  uint8_t *RxBuffer;
  uint8_t *TxBuffer;
  uint32_t RxLength;
  uint32_t TxLength;
  __IO uint32_t TxState;
  __IO uint32_t RxState;
} USBD_DUALCDC_PortHandleTypeDef;

/** Class instance data (must fit into MAX_STATIC_ALLOC_SIZE). */
typedef struct
{
  USBD_DUALCDC_PortHandleTypeDef port[DUALCDC_PORT_COUNT];
  uint8_t data[DUALCDC_CMD_PACKET_SIZE];
} USBD_DUALCDC_HandleTypeDef;

/** Application callbacks, one set shared by all ports (port is a parameter). */
typedef struct
{
  int8_t (*Init)(uint8_t port);
  int8_t (*DeInit)(uint8_t port);
  int8_t (*Control)(uint8_t port, uint8_t cmd, uint8_t *pbuf, uint16_t len);
  int8_t (*Receive)(uint8_t port, uint8_t *Buf, uint32_t *Len);
} USBD_DUALCDC_ItfTypeDef;

extern USBD_ClassTypeDef USBD_DUALCDC;

uint8_t USBD_DUALCDC_RegisterInterface(USBD_HandleTypeDef *pdev,
                                       USBD_DUALCDC_ItfTypeDef *fops);

uint8_t USBD_DUALCDC_SetTxBuffer(USBD_HandleTypeDef *pdev,
                                 uint8_t *pbuf, uint16_t len, uint8_t port);
uint8_t USBD_DUALCDC_SetRxBuffer(USBD_HandleTypeDef *pdev,
                                 uint8_t *pbuf, uint8_t port);
uint8_t USBD_DUALCDC_TransmitPacket(USBD_HandleTypeDef *pdev, uint8_t port);
uint8_t USBD_DUALCDC_ReceivePacket(USBD_HandleTypeDef *pdev, uint8_t port);
uint8_t USBD_DUALCDC_Transmit_FS(USBD_HandleTypeDef *pdev,
                                 uint8_t *Buf, uint16_t Len, uint8_t port);
uint8_t USBD_DUALCDC_TxReady(USBD_HandleTypeDef *pdev, uint8_t port);

#ifdef __cplusplus
}
#endif

#endif /* __USBD_DUAL_CDC_H */
