/* USER CODE BEGIN Header */
/**
  ******************************************************************************
  * @file    usart.h
  * @brief   Minimal USART1 driver used by the bootloader.
  *
  * The bootloader has to fit in 18 KB of the 64 KB flash of an STM32F103C8, and
  * the HAL UART driver (plus the DMA driver it references) costs about 6.7 KB.
  * A bootloader console only needs 115200 8N1, one interrupt driven receive and
  * one blocking transmit, so it drives the peripheral registers directly.
  ******************************************************************************
  */
/* USER CODE END Header */
/* Define to prevent recursive inclusion -------------------------------------*/
#ifndef __USART_H__
#define __USART_H__

#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

/** @brief Baud rate the console and the YModem receiver use. */
#define USART1_BAUD_RATE 115200U

/** @brief Bring up USART1 on PA9 (TX) / PA10 (RX) and enable the RX interrupt. */
void Usart1_Init(void);

/** @brief Shut USART1 down again (used right before jumping to the app). */
void Usart1_DeInit(void);

/** @brief Blocking transmit; never blocks for ever, it simply gives up. */
void Usart1_Write(const uint8_t *data, uint32_t len);

/** @brief USART1 interrupt handler, feeds the shell receive ring. */
void Usart1_IrqHandler(void);

#ifdef __cplusplus
}
#endif

#endif /* __USART_H__ */
