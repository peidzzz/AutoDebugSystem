/**
  ******************************************************************************
  * @file    led.h
  * @brief   PC13 LED blink control (1 Hz by default, frequency settable).
  ******************************************************************************
  */

#ifndef __LED_H__
#define __LED_H__

#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

/** Maximum blink frequency accepted (Hz). */
#define LED_MAX_FREQUENCY_HZ   500U

/**
  * @brief  Create and start the LED blink software timer (1 Hz).
  */
void LED_Init(void);

/**
  * @brief  Change the blink frequency.
  * @param  freqHz blink frequency in Hz (full on/off cycles per second),
  *               0 stops the LED (idle low level).
  */
void LED_SetFrequency(uint32_t freqHz);

/**
  * @brief  Get the currently configured blink frequency (Hz), 0 = off.
  */
uint32_t LED_GetFrequency(void);

#ifdef __cplusplus
}
#endif

#endif /* __LED_H__ */
