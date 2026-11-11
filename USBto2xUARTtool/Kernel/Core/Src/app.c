/**
  ******************************************************************************
  * @file    app.c
  * @brief   Application initialisation: LED, UART bridge, shell, USB.
  ******************************************************************************
  */

/* Includes ------------------------------------------------------------------*/
#include "app.h"
#include "led.h"
#include "bridge.h"
#include "shell_port.h"
#include "usb_device.h"
#include "usart.h"

#include <string.h>

/* Private defines -----------------------------------------------------------*/
/* UART1 only carries the boot trace of the application, it is never a console.
   Set to 0 to remove the trace completely. */
#ifndef APP_BOOT_TRACE
#define APP_BOOT_TRACE 1
#endif

/* Print the stacked frame when a hard fault happens (UART1, register access
   only).  Set to 1 while chasing a fault; the reporter plus its strings cost
   roughly 700 bytes and this build has only ~90 bytes of flash to spare. */
#ifndef APP_FAULT_TRACE
#define APP_FAULT_TRACE 0
#endif

/* Public functions ----------------------------------------------------------*/

/**
  * @brief  Current core clock in Hz, derived from the RCC status bits only.
  * @note   Needed before HAL_Init(), i.e. before HAL_RCC_GetHCLKFreq() is usable.
  */
static uint32_t App_ClockHz(void)
{
  uint32_t source = RCC->CFGR & RCC_CFGR_SWS;

  if (source == RCC_CFGR_SWS_PLL)
  {
    return 72000000U;
  }
  if (source == RCC_CFGR_SWS_HSE)
  {
    return 8000000U;
  }

  return 8000000U;
}

void App_EarlyTrace(const char *text)
{
  uint32_t clock = App_ClockHz();
  uint32_t index = 0U;

  if (text == NULL)
  {
    return;
  }

  /* UART1 on PA9/PA10, set up with register writes so that it also works when
     called as the very first statement of main(). */
  RCC->APB2ENR |= RCC_APB2ENR_IOPAEN | RCC_APB2ENR_USART1EN;

  /* PA9 = alternate function push pull 50 MHz, PA10 = floating input. */
  GPIOA->CRH = (GPIOA->CRH & ~((0xFU << 4) | (0xFU << 8))) |
               (0xBU << 4) | (0x4U << 8);

  USART1->CR1 = 0U;
  USART1->BRR = (clock + (115200U / 2U)) / 115200U;
  USART1->CR2 = 0U;
  USART1->CR3 = 0U;
  USART1->CR1 = USART_CR1_UE | USART_CR1_TE;

  while (text[index] != '\0')
  {
    while ((USART1->SR & USART_SR_TXE) == 0U)
    {
    }
    USART1->DR = (uint16_t)(uint8_t)text[index];
    index++;
  }

  while ((USART1->SR & USART_SR_TC) == 0U)
  {
  }
}

/* Fault reporter -------------------------------------------------------------*/
/* Called from HardFault_Handler (see stm32f1xx_it.c) with the stacked exception
   frame.  Everything here uses register access only: a fault can happen before
   HAL, FreeRTOS or even the C runtime are usable. */

#if (APP_FAULT_TRACE != 0)
static const char appHexDigits[] = "0123456789ABCDEF";

static void App_FaultLine(const char *name, uint32_t value)
{
  char line[24];
  uint32_t index = 0U;
  int32_t shift;

  while ((name[index] != '\0') && (index < 10U))
  {
    line[index] = name[index];
    index++;
  }
  line[index++] = ' ';
  line[index++] = '0';
  line[index++] = 'x';
  for (shift = 28; shift >= 0; shift -= 4)
  {
    line[index++] = appHexDigits[(value >> (uint32_t)shift) & 0x0FU];
  }
  line[index++] = '\r';
  line[index++] = '\n';
  line[index] = '\0';

  App_EarlyTrace(line);
}
#endif

void App_ReportFault(uint32_t *frame, uint32_t excReturn)
{
#if (APP_FAULT_TRACE != 0)
  volatile uint32_t *cfsr = (volatile uint32_t *)0xE000ED28U;
  volatile uint32_t *hfsr = (volatile uint32_t *)0xE000ED2CU;

  App_EarlyTrace("\r\n[app] HARD FAULT\r\n");
  App_FaultLine("pc", frame[6]);
  App_FaultLine("lr", frame[5]);
  App_FaultLine("psr", frame[7]);
  App_FaultLine("sp", (uint32_t)frame);
  App_FaultLine("cfsr", *cfsr);
  App_FaultLine("hfsr", *hfsr);
  App_FaultLine("exc", excReturn);
#else
  (void)frame;
  (void)excReturn;
#endif

  for (;;)
  {
  }
}

void App_BootLog(const char *text)
{
#if (APP_BOOT_TRACE != 0)
  if (text != NULL)
  {
    /* huart1 is in reset state (gState == HAL_UART_STATE_RESET) when this runs
       before MX_USART1_UART_Init(), HAL then returns immediately. */
    (void)HAL_UART_Transmit(&huart1, (const uint8_t *)text,
                            (uint16_t)strlen(text), 100U);
  }
#else
  (void)text;
#endif
}

void App_BlinkBurst(uint32_t count)
{
  uint32_t i;

  for (i = 0U; i < (count * 2U); i++)
  {
    HAL_GPIO_TogglePin(LED_GPIO_Port, LED_Pin);
    HAL_Delay(80U);
  }

  /* Leave the LED in the state the bootloader hands over (on, active low). */
  HAL_GPIO_WritePin(LED_GPIO_Port, LED_Pin, GPIO_PIN_RESET);
}

/**
  * @brief  Make the host see a real USB disconnect before the device starts.
  * @note   This board has a hard-wired 1.5k pull-up on D+ (PA12), so the host
  *         detects a device as soon as the board is powered - while this
  *         bootloader is still counting down and no firmware answers.  Windows
  *         then fails with "device descriptor request failed", disables the
  *         port and never retries, because the pull-up keeps D+ high across
  *         every MCU reset.  Driving PA12 low for a moment produces the
  *         disconnect the host needs, so it enumerates again as soon as the
  *         USB device is ready.
  */
static void App_UsbReconnect(void)
{
  GPIO_InitTypeDef init = {0};

  __HAL_RCC_GPIOA_CLK_ENABLE();

  init.Pin = GPIO_PIN_12;
  init.Mode = GPIO_MODE_OUTPUT_PP;
  init.Pull = GPIO_NOPULL;
  init.Speed = GPIO_SPEED_FREQ_LOW;
  HAL_GPIO_Init(GPIOA, &init);
  HAL_GPIO_WritePin(GPIOA, GPIO_PIN_12, GPIO_PIN_RESET);

  HAL_Delay(200U);

  init.Mode = GPIO_MODE_INPUT;
  HAL_GPIO_Init(GPIOA, &init);
  HAL_Delay(10U);
}

void App_Init(void)
{
  App_BootLog("[app] App_Init: USB ...\r\n");

  /* The USB device is started first: the letter-shell console lives on the
     third virtual COM port, so the device must be able to enumerate while the
     shell prints its banner. */
  App_UsbReconnect();
  MX_USB_DEVICE_Init();

  App_BootLog("[app] USB started, bridge ...\r\n");
  Bridge_Init();

  App_BootLog("[app] bridge ready, LED ...\r\n");
  LED_Init();

  App_BootLog("[app] LED ready, shell ...\r\n");
  Shell_Init();

  App_BootLog("[app] shell ready: console on the 3rd virtual COM port\r\n");
}
