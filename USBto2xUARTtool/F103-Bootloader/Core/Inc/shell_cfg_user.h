/**
  ******************************************************************************
  * @file    shell_cfg_user.h
  * @brief   letter-shell user configuration for the F103 bootloader.
  *
  * The bootloader has no RTOS: the shell is driven in polling mode from the
  * main loop with shellHandler(), so both the task loop and the lock hooks are
  * disabled.
  ******************************************************************************
  */

#ifndef __SHELL_CFG_USER_H__
#define __SHELL_CFG_USER_H__

#include "stm32f1xx_hal.h"

#ifdef __cplusplus
extern "C" {
#endif

/* The shell is not driven by shellTask(); the main loop feeds bytes to
   shellHandler() instead, so the blocking task loop is not needed. */
#define SHELL_TASK_WHILE              0

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
#define SHELL_HISTORY_MAX_NUMBER      3
#define SHELL_PRINT_BUFFER            128
#define SHELL_SCAN_BUFFER             0
/* The ASCII art banner costs about 400 bytes of flash; main.c prints its own
   one line build stamp instead. */
#define SHELL_SHOW_INFO               0
#define SHELL_CLS_WHEN_LOGIN          0
#define SHELL_DEFAULT_USER            "letter"
#define SHELL_DEFAULT_USER_PASSWORD   ""

/* System tick is needed for double-tab completion timing. */
#define SHELL_GET_TICK()              HAL_GetTick()

/* Single threaded: no lock hooks. */
#define SHELL_USING_LOCK              0

#ifdef __cplusplus
}
#endif

#endif /* __SHELL_CFG_USER_H__ */
