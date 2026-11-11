/**
  ******************************************************************************
  * @file    ymodem.h
  * @brief   YModem receiver that stores an application image in a flash slot.
  ******************************************************************************
  */

#ifndef __YMODEM_H__
#define __YMODEM_H__

#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

#define YMODEM_OK               0
#define YMODEM_ERR_TIMEOUT     (-1)
#define YMODEM_ERR_CANCEL      (-2)
#define YMODEM_ERR_PROTOCOL    (-3)
#define YMODEM_ERR_FLASH       (-4)
#define YMODEM_ERR_SIZE        (-5)
#define YMODEM_ERR_NAME        (-6)
/* The first payload does not start with a vector table that fits the slot:
   almost always a file that was linked for another address (for example the
   bootloader itself).  Nothing was erased. */
#define YMODEM_ERR_IMAGE       (-7)

typedef struct
{
  char name[72];        /* file name reported by the sender */
  uint32_t size;        /* number of bytes stored */
  uint32_t crc;         /* CRC32 of the stored bytes */
} YmodemInfo;

/**
  * @brief  Receive one file with YModem straight into the application slot.
  *
  * Nothing is erased when the transfer starts: the first data packet carries the
  * vector table, it is checked against the address the image was linked for and
  * only then are the pages erased (lazily, page by page) and programmed.  A
  * rejected file therefore leaves the installed application untouched.
  *
  * Typing Ctrl+C on the console aborts the transfer at any time.
  *
  * @param  info       receives the file name, the length and the CRC32
  * @param  maxSize    largest file that may be accepted
  * @param  execBase   address the image was linked for (validated with execSize)
  * @param  execSize   size of that region
  * @retval YMODEM_OK or one of the YMODEM_ERR_xxx codes
  */
int Ymodem_Receive(YmodemInfo *info, uint32_t maxSize,
                   uint32_t execBase, uint32_t execSize);

#ifdef __cplusplus
}
#endif

#endif /* __YMODEM_H__ */
