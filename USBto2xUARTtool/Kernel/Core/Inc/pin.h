/**
  ******************************************************************************
  * @file    pin.h
  * @brief   GPIO shell commands.
  *
  *   pin <pin> in           configure a pin as input
  *   pin <pin> out          configure a pin as output
  *   pin read <pin>         read the level of a pin
  *   pin write <pin> <0|1>  drive an output pin (output mode only)
  *
  * <pin> is written as port letter plus pin number, e.g. "pb5", "pa0", "pc13".
  ******************************************************************************
  */

#ifndef __PIN_H__
#define __PIN_H__

#ifdef __cplusplus
extern "C" {
#endif

/**
  * @brief  Implementation of the "pin" shell command.
  */
int Pin_ShellCmd(int argc, char *argv[]);

#ifdef __cplusplus
}
#endif

#endif /* __PIN_H__ */
