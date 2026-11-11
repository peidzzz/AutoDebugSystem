/**
  ******************************************************************************
  * @file    boot.h
  * @brief   Flash layout, image flag handling and jump code of the STM32F103
  *          single slot bootloader.
  *
  * Flash layout (64 KB part, e.g. STM32F103C8T6):
  *
  *   0x08000000 .. 0x080057FF  bootloader       (22 KB)
  *   0x08005800 .. 0x08005BFF  flag page        (1 KB, 32 records)
  *   0x08005C00 .. 0x0800FFFF  application      (41 KB, executable)
  *
  * Why only one slot: the two slot map needed 128 KB (2 x 51 KB + bootloader),
  * which does not exist on a 64 KB part.  A 64 KB part simply cannot hold two
  * copies of the 41 KB application plus a bootloader, so this build keeps a
  * single application slot and instead makes the *bootloader* the immutable
  * rescue path:
  *
  *   - the image is received straight into the application slot;
  *   - a flag record (size + CRC32) is written only after the received image
  *     verified, which makes "verified" mean "recorded";
  *   - on every start the bootloader verifies the recorded CRC before jumping,
  *     so a half written or silently corrupted image is never executed;
  *   - a power cut during a transfer leaves a broken image in the slot, but the
  *     bootloader itself is never touched: the device still answers on the
  *     console and the image can simply be sent again with "ymodem-ota k".
  *
  * The trade off versus the 128 KB design (documented in the README): during an
  * update the previous image is overwritten, so there is no automatic rollback.
  ******************************************************************************
  */

#ifndef __BOOT_H__
#define __BOOT_H__

#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

/* Flash map -----------------------------------------------------------------*/
#define BOOT_FLASH_BASE         0x08000000U
#define BOOT_FLASH_SIZE         0x00010000U   /* 64 KB */
#define BOOT_BOOTLOADER_SIZE    0x00005800U   /* 22 KB, must match the linker script */
#define BOOT_FLAG_ADDR          0x08005800U
#define BOOT_FLAG_PAGE_SIZE     0x00000400U   /* one 1 KB page */
#define BOOT_APP_ADDR           0x08005C00U   /* also the link address of the app */
#define BOOT_APP_SIZE           0x0000A400U   /* 41 KB, ends at 0x08010000 */
#define BOOT_REQUIRED_FLASH_SIZE (BOOT_APP_ADDR + BOOT_APP_SIZE - BOOT_FLASH_BASE)
#define BOOT_SRAM_BASE          0x20000000U
#define BOOT_SRAM_SIZE          0x00005000U   /* 20 KB */

/* Flag records --------------------------------------------------------------*/
#define BOOT_FLAG_MAGIC         0x424F4F54U   /* "BOOT" */
#define BOOT_FLAG_VERSION       2U
#define BOOT_FLAG_RECORD_SIZE   32U           /* sizeof(BootFlag) */
#define BOOT_FLAG_SLOTS         32U           /* records per page (1024 / 32) */

/* The image in the slot is the one described by the record. */
#define BOOT_STATE_IDLE         0U
/* Kept for compatibility with the two slot build: not used any more. */
#define BOOT_STATE_PENDING      1U

typedef struct
{
  uint32_t magic;
  uint32_t version;
  uint32_t seq;          /* increases with every committed record */
  uint32_t state;        /* BOOT_STATE_IDLE */
  uint32_t slot;         /* always 0: the single application slot */
  uint32_t image_size;   /* image length in bytes */
  uint32_t image_crc;    /* CRC32 of the image */
  uint32_t crc;          /* CRC32 of the seven words above */
} BootFlag;

/* Results -------------------------------------------------------------------*/
#define BOOT_OK                 0
#define BOOT_ERR_PARAM         (-1)
#define BOOT_ERR_FLASH         (-2)
#define BOOT_ERR_CRC           (-3)
#define BOOT_ERR_SIZE          (-4)

/* CRC32 ---------------------------------------------------------------------*/
/**
  * @brief  CRC32 (polynomial 0x04C11DB7, MSB first, no final inversion).
  * @param  crc  previous value, start with 0xFFFFFFFF
  */
uint32_t Boot_Crc32(uint32_t crc, const uint8_t *data, uint32_t len);

/* Layout --------------------------------------------------------------------*/
/** @brief  Size of the application slot in bytes. */
uint32_t Boot_SlotSize(void);

/**
  * @brief  Flash size in kilobytes as reported by the device itself
  *         (F_SIZE at 0x1FFFF7E0), clamped to a sane range.
  * @note   The layout needs 64 KB.  "bootinfo" prints this value so a device
  *         with less flash cannot be mistaken for a working one.
  */
uint32_t Boot_FlashSizeKb(void);

/**
  * @brief  Kilobytes the configured layout needs (64 for the map above).
  */
uint32_t Boot_RequiredFlashKb(void);

/**
  * @brief  Check that [address, address + size) lies inside the flash the device
  *         really reports (F_SIZE), not just inside the assumed layout.
  * @retval 1 when the whole range exists, 0 otherwise.
  */
int Boot_RangeInFlash(uint32_t address, uint32_t size);

/* Image ---------------------------------------------------------------------*/
/**
  * @brief  Check that an image starts with a plausible Cortex-M vector table:
  *         the initial stack pointer inside SRAM and the reset handler odd (thumb)
  *         and inside the application slot.
  * @retval 1 when the vector table looks usable.
  */
int Boot_ImageValid(uint32_t address);

/** @brief  CRC32 over the image stored at "address". */
uint32_t Boot_ImageCrc(uint32_t address, uint32_t size);

/** @brief  Copy the initial SP and the reset handler out of a vector table. */
void Boot_ReadVector(const uint8_t *vector, uint32_t *stackPointer, uint32_t *resetHandler);

/**
  * @brief  Apply the Boot_ImageValid() rules to a vector table that is still in
  *         RAM, i.e. to the first payload of a transfer.  It lets the receiver
  *         reject a file that was linked for another address before a single
  *         byte of the installed application is erased.
  * @param  vector   at least 8 bytes, initial SP first
  * @param  imageSize  length of the whole image, checked against execSize
  * @param  execBase   address the image was linked for
  * @param  execSize   size of that region
  * @retval 1 when the vector table looks usable.
  */
int Boot_VectorValidRange(const uint8_t *vector, uint32_t imageSize,
                          uint32_t execBase, uint32_t execSize);

/**
  * @brief  Start a new image transfer.
  * @param  size  length of the image that is going to be written
  * @note   The pages are erased lazily by Boot_WriteAppChunk(), one page at a
  *         time (about 20 ms each), so the sender never waits for a long erase
  *         and a rejected file does not touch the installed application.
  */
int Boot_BeginAppWrite(uint32_t size);

/**
  * @brief  Program a block of the image; offsets are 0 based and increasing.
  * @param  offset  byte offset inside the application slot
  */
int Boot_WriteAppChunk(uint32_t offset, const uint8_t *data, uint32_t len);

/* Flags ---------------------------------------------------------------------*/
/**
  * @brief  Find the newest valid flag record in the flag page.
  * @retval 1 when a valid record was found, 0 when the page holds none.
  */
int Boot_ReadFlag(BootFlag *flag);

/**
  * @brief  Commit a flag record.  Append only: the new record goes into the next
  *         free 32 byte slot, so a power cut while writing it cannot destroy the
  *         record that is already there.  The page is only erased when it is
  *         full, and even then a missing record is not fatal (see Boot_ReadFlag
  *         users: the image is then checked by its vector table only).
  * @retval BOOT_OK or BOOT_ERR_FLASH when the record could not be verified.
  */
int Boot_WriteFlag(BootFlag *flag);

/* Boot ----------------------------------------------------------------------*/
/**
  * @brief  Jump to the application.  Never returns.
  */
void Boot_JumpToApp(void);

/** @brief  Reset the MCU. */
void Boot_Reset(void);

/* Diagnostics ---------------------------------------------------------------*/
/**
  * @brief  Print the cause of the last reset ("power-on", "pin", "software",
  *         "watchdog", ...) and clear the flags.  Called once at start-up, it
  *         tells whether the device really restarted and why.
  */
void Boot_PrintResetCause(void);

/* Bootloader update ---------------------------------------------------------*/
/**
  * @brief  Replace the bootloader with the image staged in the application slot.
  *
  * The copy runs from a routine placed in RAM, because the flash it erases
  * contains the caller.  When this function returns the update did not start;
  * on success the device resets itself into the new bootloader and this function
  * never returns.
  *
  * @param  size       length of the staged image (page rounded internally)
  * @param  sourceCrc  CRC32 the staged image must have
  * @retval BOOT_OK never (kept for the compiler), or an error code
  */
int Boot_ApplyBootloaderUpdate(uint32_t size, uint32_t sourceCrc);

#ifdef __cplusplus
}
#endif

#endif /* __BOOT_H__ */
