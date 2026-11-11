/**
  ******************************************************************************
  * @file    led.c
  * @brief   PC13 LED blink generator (FreeRTOS software timer).
  ******************************************************************************
  */

/* Includes ------------------------------------------------------------------*/
#include "led.h"
#include "main.h"

#include "FreeRTOS.h"
#include "timers.h"

/* Private variables ---------------------------------------------------------*/
static TimerHandle_t ledTimer = NULL;
static volatile uint32_t ledFrequencyHz = 1U;

/* Private functions ---------------------------------------------------------*/

static void LED_TimerCallback(TimerHandle_t xTimer)
{
  (void)xTimer;
  HAL_GPIO_TogglePin(LED_GPIO_Port, LED_Pin);
}

static uint32_t LED_HalfPeriodTicks(uint32_t frequencyHz)
{
  uint32_t halfPeriodMs;

  if (frequencyHz == 0U)
  {
    return 0U;
  }
  if (frequencyHz > LED_MAX_FREQUENCY_HZ)
  {
    frequencyHz = LED_MAX_FREQUENCY_HZ;
  }

  halfPeriodMs = 500U / frequencyHz;
  if (halfPeriodMs == 0U)
  {
    halfPeriodMs = 1U;
  }
  return pdMS_TO_TICKS(halfPeriodMs);
}

/* Public functions ----------------------------------------------------------*/

void LED_Init(void)
{
  ledTimer = xTimerCreate("led", pdMS_TO_TICKS(500U), pdTRUE, NULL,
                          LED_TimerCallback);
  configASSERT(ledTimer != NULL);

  ledFrequencyHz = 1U;
  (void)xTimerStart(ledTimer, 0U);
}

void LED_SetFrequency(uint32_t frequencyHz)
{
  TickType_t period;

  if (ledTimer == NULL)
  {
    return;
  }

  if (frequencyHz == 0U)
  {
    (void)xTimerStop(ledTimer, 0U);
    ledFrequencyHz = 0U;
    HAL_GPIO_WritePin(LED_GPIO_Port, LED_Pin, GPIO_PIN_RESET);
    return;
  }

  if (frequencyHz > LED_MAX_FREQUENCY_HZ)
  {
    frequencyHz = LED_MAX_FREQUENCY_HZ;
  }

  period = LED_HalfPeriodTicks(frequencyHz);
  ledFrequencyHz = frequencyHz;

  /* xTimerChangePeriod also (re)starts the timer. */
  (void)xTimerChangePeriod(ledTimer, period, 0U);
}

uint32_t LED_GetFrequency(void)
{
  return ledFrequencyHz;
}
