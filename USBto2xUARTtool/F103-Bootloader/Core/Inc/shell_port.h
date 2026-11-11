/**
  ******************************************************************************
  * @file    shell_port.h
  * @brief   letter-shell console on USART1 for the F103 bootloader.
  ******************************************************************************
  */

#ifndef __SHELL_PORT_H__
#define __SHELL_PORT_H__

#include <stdint.h>

#include "shell.h"

#ifdef __cplusplus
extern "C" {
#endif

/** The one and only shell instance, valid after Shell_Init(). */
extern Shell *shellConsole;

/**
  * @brief  Start interrupt driven reception on USART1 without touching
  *         letter-shell.  Called before the boot countdown so keys pressed
  *         while the device is about to boot are not lost.
  */
void Shell_RxInit(void);

/**
  * @brief  Set up USART1 reception and the letter-shell instance.  The banner
  *         and the prompt are printed immediately, so the user sees the
  *         console as soon as the bootloader starts.
  */
void Shell_Init(void);

/**
  * @brief  Feed received bytes to letter-shell forever.  Never returns.
  */
void Shell_Loop(void);

/** @brief  Push a received byte (interrupt context). */
void Shell_RxByte(uint8_t byte);

/** @brief  Write raw bytes to the console (used by the YModem receiver). */
void Shell_WriteRaw(const uint8_t *data, uint32_t len);

/**
  * @brief  Print formatted text; forwards to shellPrint(), so the letter-shell
  *         formatting rules apply and no printf machinery is needed.
  */
#define Shell_Printf(...)                                                   \
  do                                                                        \
  {                                                                         \
    if (shellConsole != NULL)                                               \
    {                                                                       \
      shellPrint(shellConsole, __VA_ARGS__);                                \
    }                                                                       \
  } while (0)

/**
  * @brief  Read one byte from the console input ring.
  * @retval 1 on success, 0 when the timeout expired.
  */
int Shell_ReadByte(uint8_t *byte, uint32_t timeoutMs);

/** @brief  1 when at least one byte is waiting, 0 otherwise (does not consume). */
int Shell_HasByte(void);

/**
  * @brief  Look at the next input byte without removing it.
  * @retval 1 on success, 0 when the ring is empty.
  */
int Shell_PeekByte(uint8_t *byte);

/** @brief  Discard all pending input bytes. */
void Shell_Flush(void);

#ifdef __cplusplus
}
#endif

#endif /* __SHELL_PORT_H__ */
