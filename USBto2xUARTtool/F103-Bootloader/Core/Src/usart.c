/* USER CODE BEGIN Header */
/**
  ******************************************************************************
  * @file    usart.c
  * @brief   Minimal USART1 driver for the bootloader (115200 8N1, no HAL UART).
  *
  * Why not the HAL driver: the bootloader must fit into 18 KB of the 64 KB flash
  * of an STM32F103C8T6, and stm32f1xx_hal_uart.c plus the DMA driver it
  * references cost about 6.7 KB of that.  The console needs nothing more than
  * "115200 8N1, interrupt receive, blocking transmit", so the registers are
  * driven directly here.
  *
  * The receive interrupt hands every byte to Shell_RxByte(), the same entry point
  * the HAL callback used, so the ring buffer logic in shell_port.c is unchanged.
  ******************************************************************************
  */
/* USER CODE END Header */
/* Includes ------------------------------------------------------------------*/
#include <stddef.h>

#include "main.h"
#include "usart.h"
#include "shell_port.h"

/* USER CODE BEGIN 0 */

/* Every busy wait below gives up after this many polls instead of hanging the
   bootloader for ever when there is no receiver on the other end. */
#define USART1_POLL_LIMIT 4000000U

/* USER CODE END 0 */

/* USER CODE BEGIN 1 */

void Usart1_Init(void)
{
  /* PA9 = TX (alternate function push-pull, 50 MHz), PA10 = RX (input with pull-up).
     Both pins live in the high configuration register: PA9 uses bits 7:4 and
     PA10 bits 11:8: 0xB = output 50 MHz AF push-pull, 0x8 = input with pull-up/down where ODR picks the direction (ODR=1 -> pull-up). The pull-up matters: a floating RX pin is high impedance and happily receives the neighbouring TX trace, which looks like the port echoing its own data back. */
  RCC->APB2ENR |= RCC_APB2ENR_IOPAEN | RCC_APB2ENR_USART1EN;
  (void)RCC->APB2ENR; /* let the clock enable settle before touching the pins */

  GPIOA->CRH = (GPIOA->CRH & ~(0xFFUL << 4)) | (0xBUL << 4) | (0x8UL << 8);
  /* F1 chooses pull-up vs pull-down from the ODR bit: 1 = pull-up. */
  GPIOA->BSRR = GPIO_PIN_10;

  /* USARTDIV * 16 = fPCLK2 / baud.  With fPCLK2 = 72 MHz and 115200 Bd this is
     625 (0x271), which is also what integer rounding of the division gives. */
  USART1->BRR = (uint16_t)((SystemCoreClock + (USART1_BAUD_RATE / 2U)) / USART1_BAUD_RATE);
  USART1->CR2 = 0U;
  USART1->CR3 = 0U;
  USART1->CR1 = USART_CR1_UE | USART_CR1_TE | USART_CR1_RE | USART_CR1_RXNEIE;

  NVIC_SetPriority(USART1_IRQn, 5U);
  NVIC_EnableIRQ(USART1_IRQn);
}

void Usart1_DeInit(void)
{
  NVIC_DisableIRQ(USART1_IRQn);
  USART1->CR1 = 0U;
  RCC->APB2ENR &= ~RCC_APB2ENR_USART1EN;
}

void Usart1_Write(const uint8_t *data, uint32_t len)
{
  uint32_t index;

  if (data == NULL)
  {
    return;
  }

  for (index = 0U; index < len; index++)
  {
    uint32_t poll = 0U;

    while ((USART1->SR & USART_SR_TXE) == 0U)
    {
      if (++poll > USART1_POLL_LIMIT)
      {
        return; /* nobody is listening, do not stall the bootloader */
      }
    }

    USART1->DR = data[index];
  }

  /* Wait for the last byte to leave the shift register before the caller
     (for example the jump to the application) tears the peripheral down. */
  {
    uint32_t poll = 0U;
    while ((USART1->SR & USART_SR_TC) == 0U)
    {
      if (++poll > USART1_POLL_LIMIT)
      {
        return;
      }
    }
  }
}

void Usart1_IrqHandler(void)
{
  uint32_t status = USART1->SR;

  /* Reading SR followed by DR clears RXNE and also ORE/FE/NE/PE, so an overrun
     cannot wedge the receiver.  Only bytes that actually arrived are queued. */
  if ((status & (USART_SR_RXNE | USART_SR_ORE)) != 0U)
  {
    uint8_t byte = (uint8_t)(USART1->DR & 0xFFU);

    if ((status & USART_SR_RXNE) != 0U)
    {
      Shell_RxByte(byte);
    }
  }
}

/* USER CODE END 1 */
