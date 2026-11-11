/**
  ******************************************************************************
  * @file    shell_cfg_user.h
  * @brief   letter-shell user configuration for the USB-to-2xUART tool.
  *
  * This header is included by letter-shell's shell_cfg.h.  Keep the defaults
  * from shell_cfg.h unless a different behaviour is really required.
  ******************************************************************************
  */

#ifndef __SHELL_CFG_USER_H__
#define __SHELL_CFG_USER_H__

#include "stm32f1xx_hal.h"

#ifdef __cplusplus
extern "C" {
#endif

/* Shell is driven by a dedicated FreeRTOS task. */
#define SHELL_TASK_WHILE              1

/* Commands are exported into the "shellCommand" linker section. */
#define SHELL_USING_CMD_EXPORT        1

/* No companion objects / dynamic allocation needed. */
#define SHELL_USING_COMPANION         0

/* Accept both CR and LF as line terminator (any terminal works). */
#define SHELL_ENTER_LF                1
#define SHELL_ENTER_CR                1
#define SHELL_ENTER_CRLF              0

/* Terminal parameters. */
#define SHELL_PARAMETER_MAX_NUMBER    8
#define SHELL_HISTORY_MAX_NUMBER      5
#define SHELL_PRINT_BUFFER            128
#define SHELL_SCAN_BUFFER             0
#define SHELL_SHOW_INFO               1
#define SHELL_CLS_WHEN_LOGIN          1
#define SHELL_DEFAULT_USER            "letter"
#define SHELL_DEFAULT_USER_PASSWORD   ""

/* System tick is needed for double-tab completion timing. */
#define SHELL_GET_TICK()              HAL_GetTick()

/* The shell is only used from one task, but keep the lock hooks to make the
   protection explicit and safe against future users of shellPrint(). */
#define SHELL_USING_LOCK              1

#ifdef __cplusplus
}
#endif

#endif /* __SHELL_CFG_USER_H__ */
