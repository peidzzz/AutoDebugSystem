/**
  ******************************************************************************
  * @file    shell_port.h
  * @brief   letter-shell port on the third virtual COM port (USB CDC, port 2).
  ******************************************************************************
  */

#ifndef __SHELL_PORT_H__
#define __SHELL_PORT_H__

#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

/**
  * @brief  Initialise letter-shell and start the shell task.
  */
void Shell_Init(void);

/**
  * @brief  Push a block of received bytes into the shell input ring.
  *         Called from the USB interrupt, therefore interrupt safe.
  */
void Shell_RxBlock(const uint8_t *data, uint32_t len);

/**
  * @brief  Push one received byte into the shell input ring.
  */
void Shell_RxByte(uint8_t byte);

#ifdef __cplusplus
}
#endif

#endif /* __SHELL_PORT_H__ */
