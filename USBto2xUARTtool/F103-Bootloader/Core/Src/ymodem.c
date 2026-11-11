/**
  ******************************************************************************
  * @file    ymodem.c
  * @brief   YModem receiver: streams the blocks straight into the application slot.
  *
  * The receiver drives the protocol (it sends 'C' and waits for the header),
  * programs every accepted block immediately and returns the CRC32 and the
  * length of the bytes it stored.  Image validation and flag handling are done
  * by the caller.
  ******************************************************************************
  */

/* Includes ------------------------------------------------------------------*/
#include "ymodem.h"
#include "boot.h"
#include "shell_port.h"

#include <stdlib.h>
#include <string.h>

/* Private defines -----------------------------------------------------------*/
#define YMODEM_SOH            0x01U
#define YMODEM_STX            0x02U
#define YMODEM_EOT            0x04U
#define YMODEM_ACK            0x06U
#define YMODEM_NAK            0x15U
#define YMODEM_CAN            0x18U
#define YMODEM_CTRL_C         0x03U   /* typed on the console to abort the transfer */
#define YMODEM_CRC_REQUEST    0x43U   /* 'C' */

#define YMODEM_PACKET_TIMEOUT 3000U
#define YMODEM_HEADER_TIMEOUT 15000U
#define YMODEM_MAX_RETRIES    10U
#define YMODEM_PROGRESS_STEP  8U      /* blocks between two progress updates */

/* Internal results of readPacket() (packet lengths are >= 0). */
#define YMODEM_RX_TIMEOUT     (-1)
#define YMODEM_RX_CANCEL      (-2)
#define YMODEM_RX_EOT         (-3)
#define YMODEM_RX_ERROR       (-4)

/* Private variables ---------------------------------------------------------*/
static uint8_t packetBuffer[1024];

/* Private functions ---------------------------------------------------------*/

static uint16_t Ymodem_Crc16(const uint8_t *data, uint32_t len)
{
  uint16_t crc = 0U;
  uint32_t i;
  uint8_t bit;

  for (i = 0U; i < len; i++)
  {
    crc ^= (uint16_t)((uint16_t)data[i] << 8);
    for (bit = 0U; bit < 8U; bit++)
    {
      crc = (crc & 0x8000U) ? (uint16_t)((crc << 1) ^ 0x1021U) : (uint16_t)(crc << 1);
    }
  }

  return crc;
}

static void Ymodem_SendByte(uint8_t byte)
{
  Shell_WriteRaw(&byte, 1U);
}

static void Ymodem_Cancel(void)
{
  Ymodem_SendByte(YMODEM_CAN);
  Ymodem_SendByte(YMODEM_CAN);
}

/**
  * @brief  Receive one YModem packet.
  * @param  sequence  receives the packet sequence number
  * @retval number of payload bytes (>= 0), or one of the YMODEM_RX_xxx codes
  */
static int Ymodem_ReadPacket(uint8_t *sequence, uint32_t timeoutMs)
{
  uint8_t header;
  uint8_t seq;
  uint8_t seqInverted;
  uint8_t crcHigh;
  uint8_t crcLow;
  uint32_t size;
  uint32_t i;
  uint16_t crc;

  if (Shell_ReadByte(&header, timeoutMs) == 0)
  {
    return YMODEM_RX_TIMEOUT;
  }

  if (header == YMODEM_EOT)
  {
    return YMODEM_RX_EOT;
  }
  if ((header == YMODEM_CAN) || (header == YMODEM_CTRL_C))
  {
    return YMODEM_RX_CANCEL;
  }
  if (header == YMODEM_SOH)
  {
    size = 128U;
  }
  else if (header == YMODEM_STX)
  {
    size = 1024U;
  }
  else
  {
    return YMODEM_RX_ERROR;
  }

  if ((Shell_ReadByte(&seq, YMODEM_PACKET_TIMEOUT) == 0) ||
      (Shell_ReadByte(&seqInverted, YMODEM_PACKET_TIMEOUT) == 0))
  {
    return YMODEM_RX_TIMEOUT;
  }

  if ((uint8_t)(seq ^ seqInverted) != 0xFFU)
  {
    return YMODEM_RX_ERROR;
  }

  for (i = 0U; i < size; i++)
  {
    if (Shell_ReadByte(&packetBuffer[i], YMODEM_PACKET_TIMEOUT) == 0)
    {
      return YMODEM_RX_TIMEOUT;
    }
  }

  if ((Shell_ReadByte(&crcHigh, YMODEM_PACKET_TIMEOUT) == 0) ||
      (Shell_ReadByte(&crcLow, YMODEM_PACKET_TIMEOUT) == 0))
  {
    return YMODEM_RX_TIMEOUT;
  }

  crc = (uint16_t)(((uint16_t)crcHigh << 8) | crcLow);
  if (crc != Ymodem_Crc16(packetBuffer, size))
  {
    return YMODEM_RX_ERROR;
  }

  *sequence = seq;
  return (int)size;
}

static int Ymodem_NameAccepted(const char *name)
{
  size_t length = strlen(name);

  if (length < 5U)
  {
    return 0;
  }

  /* Only .bin images are accepted. */
  if ((name[length - 4U] != '.') ||
      ((name[length - 3U] != 'b') && (name[length - 3U] != 'B')) ||
      ((name[length - 2U] != 'i') && (name[length - 2U] != 'I')) ||
      ((name[length - 1U] != 'n') && (name[length - 1U] != 'N')))
  {
    return 0;
  }

  return 1;
}

/* Public functions ----------------------------------------------------------*/

int Ymodem_Receive(YmodemInfo *info, uint32_t maxSize,
                   uint32_t execBase, uint32_t execSize)
{
  uint8_t sequence = 0U;
  uint8_t retries;
  int length;
  uint32_t expected;
  uint32_t written = 0U;
  uint32_t totalSize = 0U;
  uint32_t crc = 0xFFFFFFFFU;
  uint32_t nameLength = 0U;
  char sizeText[16];
  uint8_t failures;
  int headerReceived = 0;

  if ((info == NULL) || (maxSize == 0U))
  {
    return YMODEM_ERR_PROTOCOL;
  }

  info->name[0] = '\0';
  info->size = 0U;
  info->crc = 0U;

  /* Phase 1: wait for the header packet that carries name and size. */
  for (retries = 0U; retries < YMODEM_MAX_RETRIES; retries++)
  {
    Ymodem_SendByte(YMODEM_CRC_REQUEST);

    length = Ymodem_ReadPacket(&sequence, YMODEM_HEADER_TIMEOUT);
    if (length == YMODEM_RX_CANCEL)
    {
      return YMODEM_ERR_CANCEL;
    }
    if (length < 0)
    {
      continue;
    }
    if ((sequence != 0U) || (length == 0))
    {
      continue;
    }
    headerReceived = 1;
    break;
  }

  if (headerReceived == 0)
  {
    return YMODEM_ERR_TIMEOUT;
  }

  /* Name is the leading NUL terminated string, the size follows it. */
  while ((nameLength + 1U < sizeof(info->name)) &&
         (nameLength < (uint32_t)length) &&
         (packetBuffer[nameLength] != 0U))
  {
    info->name[nameLength] = (char)packetBuffer[nameLength];
    nameLength++;
  }
  info->name[nameLength] = '\0';

  {
    uint32_t index = nameLength + 1U;
    uint32_t count = 0U;

    while ((index < (uint32_t)length) &&
           (count < (sizeof(sizeText) - 1U)) &&
           (packetBuffer[index] != 0U) &&
           (packetBuffer[index] != ' '))
    {
      sizeText[count] = (char)packetBuffer[index];
      count++;
      index++;
    }
    sizeText[count] = '\0';
  }

  totalSize = (uint32_t)strtoul(sizeText, NULL, 10);

  if (Ymodem_NameAccepted(info->name) == 0)
  {
    Ymodem_Cancel();
    return YMODEM_ERR_NAME;
  }

  if ((totalSize == 0U) || (totalSize > maxSize))
  {
    Ymodem_Cancel();
    return YMODEM_ERR_SIZE;
  }

  /* Nothing is erased yet: the first data packet carries the vector table and is
     checked before the installed application is touched. */
  Ymodem_SendByte(YMODEM_ACK);
  Ymodem_SendByte(YMODEM_CRC_REQUEST);

  /* Phase 2: receive the data blocks. */
  expected = 1U;
  failures = 0U;

  for (;;)
  {
    length = Ymodem_ReadPacket(&sequence, YMODEM_PACKET_TIMEOUT);

    if (length >= 0)
    {
      if (sequence == (uint8_t)expected)
      {
        uint32_t payload = (uint32_t)length;

        if ((written + payload) > totalSize)
        {
          payload = totalSize - written;
        }

        if (payload > 0U)
        {
          if (written == 0U)
          {
            /* First payload: check the vector table before erasing anything. */
            if (Boot_VectorValidRange(packetBuffer, totalSize,
                                      execBase, execSize) == 0)
            {
              Ymodem_Cancel();
              return YMODEM_ERR_IMAGE;
            }
            if (Boot_BeginAppWrite(totalSize) != BOOT_OK)
            {
              Ymodem_Cancel();
              return YMODEM_ERR_FLASH;
            }
          }

          if (Boot_WriteAppChunk(written, packetBuffer, payload) != BOOT_OK)
          {
            Ymodem_Cancel();
            return YMODEM_ERR_FLASH;
          }
          crc = Boot_Crc32(crc, packetBuffer, payload);
          written += payload;
        }

        expected++;
        failures = 0U;
        Ymodem_SendByte(YMODEM_ACK);

        if (((expected % YMODEM_PROGRESS_STEP) == 0U) && (totalSize != 0U))
        {
          Shell_Printf("\r  %u%%  (%u / %u bytes)   ",
                        (unsigned int)((written * 100U) / totalSize),
                        (unsigned int)written, (unsigned int)totalSize);
        }
      }
      else if (sequence == (uint8_t)(expected - 1U))
      {
        /* Duplicate of the previous block: acknowledge, do not store it. */
        Ymodem_SendByte(YMODEM_ACK);
      }
      else
      {
        Ymodem_SendByte(YMODEM_NAK);
        failures++;
        if (failures > YMODEM_MAX_RETRIES)
        {
          Ymodem_Cancel();
          return YMODEM_ERR_PROTOCOL;
        }
      }
    }
    else if (length == YMODEM_RX_EOT)
    {
      Ymodem_SendByte(YMODEM_ACK);
      break;
    }
    else if (length == YMODEM_RX_CANCEL)
    {
      return YMODEM_ERR_CANCEL;
    }
    else
    {
      Ymodem_SendByte(YMODEM_NAK);
      failures++;
      if (failures > YMODEM_MAX_RETRIES)
      {
        return YMODEM_ERR_TIMEOUT;
      }
    }
  }

  /* Phase 3: the sender closes the batch with an empty header packet. */
  Ymodem_SendByte(YMODEM_CRC_REQUEST);
  length = Ymodem_ReadPacket(&sequence, YMODEM_PACKET_TIMEOUT);
  if (length >= 0)
  {
    Ymodem_SendByte(YMODEM_ACK);
  }

  info->size = written;
  info->crc = crc ^ 0xFFFFFFFFU;

  Shell_Printf("\r\n");

  if (written != totalSize)
  {
    return YMODEM_ERR_SIZE;
  }

  return YMODEM_OK;
}
