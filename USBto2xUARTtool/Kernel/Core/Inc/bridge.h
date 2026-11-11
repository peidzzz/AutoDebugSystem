/**
  ******************************************************************************
  * @file    bridge.h
  * @brief   USB <-> UART2 / UART3 bridge.
  *
  * Virtual COM port 0 is bridged to UART2, virtual COM port 1 to UART3.
  ******************************************************************************
  */

#ifndef __BRIDGE_H__
#define __BRIDGE_H__

#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

/**
  * @brief  Start UART reception and create the two bridge tasks.
  *         Must be called after the RTOS scheduler has been started.
  */
void Bridge_Init(void);

/**
  * @brief  Called from the USB receive callback (ISR context) with a block of
  *         data coming from the host for the given port.
  */
void Bridge_UsbToUart(uint8_t port, const uint8_t *data, uint32_t len);

/**
  * @brief  CDC SET_LINE_CODING: store a new line coding for a port.  The UART
  *         is reconfigured by the port task (not from the USB ISR).
  */
void Bridge_SetLineCoding(uint8_t port, const uint8_t *coding);

/**
  * @brief  CDC GET_LINE_CODING: copy the current 7 byte line coding.
  */
void Bridge_GetLineCoding(uint8_t port, uint8_t *coding);

/**
  * @brief  CDC SET_CONTROL_LINE_STATE (DTR/RTS bits).
  */
void Bridge_SetControlLineState(uint8_t port, uint8_t state);

/**
  * @brief  Drop queued data of a port (called when the host closes/reconfigures
  *         the virtual COM port).
  */
void Bridge_Reset(uint8_t port);

/**
  * @brief  Currently active baud rate of a port.
  */
uint32_t Bridge_GetBaudRate(uint8_t port);

#ifdef __cplusplus
}
#endif

#endif /* __BRIDGE_H__ */
