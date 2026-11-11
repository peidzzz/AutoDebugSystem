/**
  ******************************************************************************
  * @file    app.h
  * @brief   Application initialisation.
  ******************************************************************************
  */

#ifndef __APP_H__
#define __APP_H__

#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

/**
  * @brief  Bring up all application features: LED blink timer, UART bridge,
  *         letter-shell console and the USB device.
  *         Call once, after the RTOS scheduler has been started.
  */
void App_Init(void);

/**
  * @brief  Write a short trace line to UART1 (115200 8N1).
  * @note   UART1 is not a console any more, it only carries this boot trace so
  *         that a board that fails to come up can be diagnosed from the same
  *         serial adapter that is used for the bootloader's YModem upload.
  *         Compile with APP_BOOT_TRACE 0 (in app.c) to remove every write.
  * @param  text  NUL terminated string; ignored when NULL or when tracing is off.
  */
void App_BootLog(const char *text);

/**
  * @brief  Blink the on board LED a few times and leave it on.
  * @note   Used as a terminal independent sign of life: the bootloader leaves
  *         the LED permanently on, so a burst of fast blinks after the jump
  *         proves that main() of the application was reached.
  * @param  count  number of blinks (each about 160 ms long)
  */
void App_BlinkBurst(uint32_t count);

/**
  * @brief  Write a trace line with nothing but register writes.
  * @note   Works before HAL_Init() and before the clock tree is configured: it
  *         re-derives the baud rate divisor from the clock source the reset
  *         control unit currently reports, so the very first statement of
  *         main() can already be seen on UART1.  Used to tell "the jump never
  *         reached main()" from "main() ran but died later".
  * @param  text  NUL terminated string; ignored when NULL.
  */
void App_EarlyTrace(const char *text);

/**
  * @brief  Dump the stacked exception frame over UART1 and stop.
  * @note   Called from HardFault_Handler() with the frame pointer and the
  *         EXC_RETURN value.  It never returns, so a hard fault leaves a
  *         readable pc/lr/psr/cfsr/hfsr line instead of a silent hang.
  *         Compile with APP_FAULT_TRACE 0 (in app.c) to make it a bare loop.
  * @param  frame      r0, r1, r2, r3, r12, lr, pc, xPSR as stacked by the CPU
  * @param  excReturn  EXC_RETURN value from LR at the time of the fault
  */
void App_ReportFault(uint32_t *frame, uint32_t excReturn);

#ifdef __cplusplus
}
#endif

#endif /* __APP_H__ */
