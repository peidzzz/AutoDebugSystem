/**
  ******************************************************************************
  * @file    usbd_dual_cdc_if.h
  * @brief   Application interface of the dual CDC class (two VCPs).
  ******************************************************************************
  */

#ifndef __USBD_DUAL_CDC_IF_H
#define __USBD_DUAL_CDC_IF_H

#ifdef __cplusplus
extern "C" {
#endif

#include "usbd_dual_cdc.h"

extern USBD_DUALCDC_ItfTypeDef USBD_Interface_fops_FS;

/**
  * @brief  Queue a block of data on one virtual COM port.
  * @param  port 0 = UART2 bridge, 1 = UART3 bridge
  * @retval USBD_OK if the transfer was started, USBD_BUSY if the previous one
  *         is still in flight, USBD_FAIL when USB is not configured.
  */
uint8_t DualCDC_Transmit(uint8_t port, uint8_t *buf, uint16_t len);

/**
  * @brief  True when the bulk IN endpoint of the port is free and a new
  *         buffer may be handed over with DualCDC_Transmit().
  */
uint8_t DualCDC_TxReady(uint8_t port);

/**
  * @brief  True when the host has configured the device (cable plugged in and
  *         enumeration finished).
  */
uint8_t DualCDC_IsConfigured(void);

#ifdef __cplusplus
}
#endif

#endif /* __USBD_DUAL_CDC_IF_H */
