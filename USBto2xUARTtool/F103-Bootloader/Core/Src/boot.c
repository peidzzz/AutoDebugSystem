/**
  ******************************************************************************
  * @file    boot.c
  * @brief   Flash layout, image flag records and jump code of the single slot
  *          bootloader for a 64 KB STM32F103 (see boot.h for the map and for why
  *          there is only one application slot).
  ******************************************************************************
  */

/* Includes ------------------------------------------------------------------*/
#include "boot.h"
#include "main.h"
#include "usart.h"

#include <stddef.h>
#include <string.h>

/* Private defines -----------------------------------------------------------*/
#define BOOT_PAGE_SIZE        0x400U
#define BOOT_FLAG_FIELD_WORDS 7U   /* words covered by flag.crc */

/* Private variables ---------------------------------------------------------*/
static uint32_t crcTable[16];
static uint8_t crcTableReady = 0U;
/* Bytes of the application slot that the current transfer has erased already. */
static uint32_t bootErasedEnd = 0U;

/* Private function prototypes -----------------------------------------------*/
static void Boot_CrcInit(void);
static int Boot_FlashErasePage(uint32_t address);
static int Boot_FlashWrite(uint32_t address, const uint8_t *data, uint32_t len);
static int Boot_FlagValid(const BootFlag *flag);
static uint32_t Boot_FlagSlotAddress(uint32_t index);
static void Boot_NvicClearAll(void);

/* CRC32 ---------------------------------------------------------------------*/

/**
  * @brief  Build the nibble table of the CRC32 polynomial 0x04C11DB7.
  */
static void Boot_CrcInit(void)
{
  uint32_t i;
  uint32_t j;
  uint32_t value;

  if (crcTableReady != 0U)
  {
    return;
  }

  for (i = 0U; i < 16U; i++)
  {
    value = i << 28;
    for (j = 0U; j < 4U; j++)
    {
      value = (value & 0x80000000U) ? ((value << 1) ^ 0x04C11DB7U) : (value << 1);
    }
    crcTable[i] = value;
  }
  crcTableReady = 1U;
}

uint32_t Boot_Crc32(uint32_t crc, const uint8_t *data, uint32_t len)
{
  uint32_t i;

  Boot_CrcInit();

  for (i = 0U; i < len; i++)
  {
    crc = (crc << 4) ^ crcTable[((crc >> 28) ^ (data[i] >> 4)) & 0x0FU];
    crc = (crc << 4) ^ crcTable[((crc >> 28) ^ (data[i] & 0x0FU)) & 0x0FU];
  }

  return crc;
}

uint32_t Boot_ImageCrc(uint32_t address, uint32_t size)
{
  return Boot_Crc32(0xFFFFFFFFU, (const uint8_t *)address, size) ^ 0xFFFFFFFFU;
}

/* Flash helpers -------------------------------------------------------------*/

static int Boot_FlashErasePage(uint32_t address)
{
  FLASH_EraseInitTypeDef erase;
  uint32_t pageError = 0U;
  HAL_StatusTypeDef status;

  erase.TypeErase = FLASH_TYPEERASE_PAGES;
  erase.PageAddress = address;
  erase.NbPages = 1U;

  __HAL_FLASH_CLEAR_FLAG(FLASH_FLAG_EOP | FLASH_FLAG_PGERR | FLASH_FLAG_WRPERR);

  status = HAL_FLASHEx_Erase(&erase, &pageError);
  return (status == HAL_OK) ? BOOT_OK : BOOT_ERR_FLASH;
}

/**
  * @brief  Program bytes into flash (half word programming, 0xFF padded) and
  *         read every half word back.
  */
static int Boot_FlashWrite(uint32_t address, const uint8_t *data, uint32_t len)
{
  uint32_t i;
  uint16_t halfword;
  HAL_StatusTypeDef status;

  for (i = 0U; i < len; i += 2U)
  {
    halfword = (uint16_t)data[i];
    halfword |= (uint16_t)((i + 1U < len) ? ((uint16_t)data[i + 1U] << 8) : 0xFF00U);

    __HAL_FLASH_CLEAR_FLAG(FLASH_FLAG_EOP | FLASH_FLAG_PGERR | FLASH_FLAG_WRPERR);
    status = HAL_FLASH_Program(FLASH_TYPEPROGRAM_HALFWORD, address + i,
                              (uint64_t)halfword);
    if (status != HAL_OK)
    {
      return BOOT_ERR_FLASH;
    }

    if (*(__IO uint16_t *)(address + i) != halfword)
    {
      return BOOT_ERR_FLASH;
    }
  }

  return BOOT_OK;
}

/* Layout --------------------------------------------------------------------*/

uint32_t Boot_SlotSize(void)
{
  return BOOT_APP_SIZE;
}

uint32_t Boot_FlashSizeKb(void)
{
  uint32_t kilobytes = (uint32_t)(*(__IO uint16_t *)0x1FFFF7E0U);

  /* A blank or unreadable register reads as 0xFFFF; keep the result in the
     range of real STM32F103 parts so that the bounds checks stay meaningful. */
  if (kilobytes > 1024U)
  {
    kilobytes = 1024U;
  }

  return kilobytes;
}

uint32_t Boot_RequiredFlashKb(void)
{
  return (uint32_t)(BOOT_REQUIRED_FLASH_SIZE / 1024U);
}

int Boot_RangeInFlash(uint32_t address, uint32_t size)
{
  uint32_t kilobytes = Boot_FlashSizeKb();
  uint32_t end = address + size;
  uint32_t flashEnd;

  if (kilobytes == 0U)
  {
    /* No usable size information: fall back to the size the layout needs. */
    kilobytes = Boot_RequiredFlashKb();
  }

  flashEnd = BOOT_FLASH_BASE + (kilobytes * 1024U);

  if (end < address) /* overflow */
  {
    return 0;
  }

  if ((address < BOOT_FLASH_BASE) || (end > flashEnd))
  {
    return 0;
  }

  return 1;
}

/* Image ---------------------------------------------------------------------*/

void Boot_ReadVector(const uint8_t *vector, uint32_t *stackPointer, uint32_t *resetHandler)
{
  if (vector == NULL)
  {
    if (stackPointer != NULL)
    {
      *stackPointer = 0U;
    }
    if (resetHandler != NULL)
    {
      *resetHandler = 0U;
    }
    return;
  }

  if (stackPointer != NULL)
  {
    *stackPointer = ((uint32_t)vector[0]) | ((uint32_t)vector[1] << 8) |
                    ((uint32_t)vector[2] << 16) | ((uint32_t)vector[3] << 24);
  }
  if (resetHandler != NULL)
  {
    *resetHandler = ((uint32_t)vector[4]) | ((uint32_t)vector[5] << 8) |
                    ((uint32_t)vector[6] << 16) | ((uint32_t)vector[7] << 24);
  }
}

/**
  * @brief  Shared rule set of Boot_ImageValid() and Boot_VectorValidRange().
  */
static int Boot_CheckVector(uint32_t stackPointer, uint32_t resetHandler,
                            uint32_t execBase, uint32_t execSize)
{
  if (stackPointer == 0xFFFFFFFFU)
  {
    return 0;
  }

  /* Initial stack pointer must point into SRAM (word aligned). */
  if ((stackPointer < (BOOT_SRAM_BASE + 0x100U)) ||
      (stackPointer > (BOOT_SRAM_BASE + BOOT_SRAM_SIZE)) ||
      ((stackPointer & 0x3U) != 0U))
  {
    return 0;
  }

  /* The reset handler must be a thumb address inside the region the image was
     linked for, which is not necessarily the region it is stored in. */
  if (((resetHandler & 0x1U) == 0U) ||
      (resetHandler < execBase) ||
      (resetHandler >= (execBase + execSize)) ||
      (resetHandler == 0xFFFFFFFFU))
  {
    return 0;
  }

  return 1;
}

int Boot_ImageValid(uint32_t address)
{
  return Boot_CheckVector(*(__IO uint32_t *)address, *(__IO uint32_t *)(address + 4U),
                          BOOT_APP_ADDR, BOOT_APP_SIZE);
}

int Boot_VectorValidRange(const uint8_t *vector, uint32_t imageSize,
                          uint32_t execBase, uint32_t execSize)
{
  uint32_t stackPointer;
  uint32_t resetHandler;

  if ((imageSize == 0U) || (imageSize > execSize) ||
      (Boot_RangeInFlash(BOOT_APP_ADDR, imageSize) == 0))
  {
    return 0;
  }

  Boot_ReadVector(vector, &stackPointer, &resetHandler);

  return Boot_CheckVector(stackPointer, resetHandler, execBase, execSize);
}

int Boot_BeginAppWrite(uint32_t size)
{
  if ((size == 0U) || (size > BOOT_APP_SIZE) ||
      (Boot_RangeInFlash(BOOT_APP_ADDR, size) == 0))
  {
    return BOOT_ERR_SIZE;
  }

  bootErasedEnd = 0U;

  return BOOT_OK;
}

int Boot_WriteAppChunk(uint32_t offset, const uint8_t *data, uint32_t len)
{
  int result;
  uint32_t required;

  if ((data == NULL) || (len == 0U) ||
      ((offset + len) > BOOT_APP_SIZE) || ((offset & 0x1U) != 0U) ||
      (Boot_RangeInFlash(BOOT_APP_ADDR + offset, len) == 0))
  {
    return BOOT_ERR_PARAM;
  }

  /* Erase every page this chunk touches before programming it. */
  required = ((offset + len) + BOOT_PAGE_SIZE - 1U) & ~(BOOT_PAGE_SIZE - 1U);

  HAL_FLASH_Unlock();

  while (bootErasedEnd < required)
  {
    result = Boot_FlashErasePage(BOOT_APP_ADDR + bootErasedEnd);
    if (result != BOOT_OK)
    {
      HAL_FLASH_Lock();
      return result;
    }
    bootErasedEnd += BOOT_PAGE_SIZE;
  }

  result = Boot_FlashWrite(BOOT_APP_ADDR + offset, data, len);
  HAL_FLASH_Lock();

  return result;
}

/* Flag records --------------------------------------------------------------*/

static int Boot_FlagValid(const BootFlag *flag)
{
  if ((flag->magic != BOOT_FLAG_MAGIC) || (flag->version != BOOT_FLAG_VERSION))
  {
    return 0;
  }

  if (Boot_Crc32(0xFFFFFFFFU, (const uint8_t *)flag,
                 BOOT_FLAG_FIELD_WORDS * sizeof(uint32_t)) != flag->crc)
  {
    return 0;
  }

  if (flag->state != BOOT_STATE_IDLE)
  {
    return 0;
  }

  if (flag->slot != 0U)
  {
    return 0;
  }

  if ((flag->image_size == 0U) || (flag->image_size > BOOT_APP_SIZE))
  {
    return 0;
  }

  return 1;
}

static uint32_t Boot_FlagSlotAddress(uint32_t index)
{
  return BOOT_FLAG_ADDR + (index * BOOT_FLAG_RECORD_SIZE);
}

int Boot_ReadFlag(BootFlag *flag)
{
  BootFlag record;
  BootFlag newest;
  uint32_t index;
  int found = 0;

  if (flag == NULL)
  {
    return 0;
  }

  memset(&newest, 0, sizeof(newest));

  for (index = 0U; index < BOOT_FLAG_SLOTS; index++)
  {
    memcpy(&record, (const void *)Boot_FlagSlotAddress(index), sizeof(record));

    if (Boot_FlagValid(&record) == 0)
    {
      continue;
    }

    if ((found == 0) || (record.seq >= newest.seq))
    {
      newest = record;
      found = 1;
    }
  }

  if (found == 0)
  {
    memset(flag, 0, sizeof(*flag));
    return 0;
  }

  *flag = newest;
  return 1;
}

int Boot_WriteFlag(BootFlag *flag)
{
  uint32_t newestIndex = 0U;
  uint32_t writeIndex;
  uint32_t address;
  int haveNewest = 0;
  int needErase = 0;
  uint32_t index;
  BootFlag record;
  BootFlag newest;
  int result;

  if (flag == NULL)
  {
    return BOOT_ERR_PARAM;
  }

  /* Find the newest record, so the new one is appended behind it. */
  for (index = 0U; index < BOOT_FLAG_SLOTS; index++)
  {
    memcpy(&record, (const void *)Boot_FlagSlotAddress(index), sizeof(record));
    if (Boot_FlagValid(&record) == 0)
    {
      continue;
    }

    if ((haveNewest == 0) || (record.seq >= newest.seq))
    {
      newest = record;
      newestIndex = index;
      haveNewest = 1;
    }
  }

  if (haveNewest == 0)
  {
    /* No record at all: start at the first slot, erasing a page that holds only
       garbage so that programming is possible. */
    writeIndex = 0U;
    memcpy(&record, (const void *)Boot_FlagSlotAddress(0U), sizeof(record));
    if ((record.magic != 0xFFFFFFFFU) || (record.version != 0xFFFFFFFFU))
    {
      needErase = 1;
    }
  }
  else
  {
    /* First still blank slot behind the newest record.  (A hole can exist when an
       earlier write was interrupted, so every slot is tested, not just the next
       one.) */
    for (index = newestIndex + 1U; index < BOOT_FLAG_SLOTS; index++)
    {
      memcpy(&record, (const void *)Boot_FlagSlotAddress(index), sizeof(record));
      if ((record.magic == 0xFFFFFFFFU) && (record.version == 0xFFFFFFFFU) &&
          (record.image_size == 0xFFFFFFFFU))
      {
        break;
      }
    }

    if (index < BOOT_FLAG_SLOTS)
    {
      writeIndex = index;
    }
    else
    {
      /* The page is full: erasing it removes the old records, but the image
         itself stays untouched and the next record describes it as well. */
      writeIndex = 0U;
      needErase = 1;
    }
  }

  flag->magic = BOOT_FLAG_MAGIC;
  flag->version = BOOT_FLAG_VERSION;
  flag->crc = Boot_Crc32(0xFFFFFFFFU, (const uint8_t *)flag,
                         BOOT_FLAG_FIELD_WORDS * sizeof(uint32_t));

  address = Boot_FlagSlotAddress(writeIndex);

  HAL_FLASH_Unlock();
  result = BOOT_OK;
  if (needErase != 0)
  {
    result = Boot_FlashErasePage(BOOT_FLAG_ADDR);
  }
  if (result == BOOT_OK)
  {
    result = Boot_FlashWrite(address, (const uint8_t *)flag, sizeof(*flag));
  }
  HAL_FLASH_Lock();

  if (result != BOOT_OK)
  {
    return result;
  }

  /* Verify what is actually stored. */
  {
    BootFlag check;
    memcpy(&check, (const void *)address, sizeof(check));
    if ((memcmp(&check, flag, sizeof(check)) != 0) || (Boot_FlagValid(&check) == 0))
    {
      return BOOT_ERR_FLASH;
    }
  }

  return BOOT_OK;
}

/* Boot ----------------------------------------------------------------------*/

static void Boot_NvicClearAll(void)
{
  uint32_t i;

  for (i = 0U; i < 8U; i++)
  {
    NVIC->ICER[i] = 0xFFFFFFFFU;
    NVIC->ICPR[i] = 0xFFFFFFFFU;
  }
}

void Boot_JumpToApp(void)
{
  uint32_t stackPointer = *(__IO uint32_t *)BOOT_APP_ADDR;
  uint32_t resetHandler = *(__IO uint32_t *)(BOOT_APP_ADDR + 4U);

  if (Boot_ImageValid(BOOT_APP_ADDR) == 0)
  {
    return;
  }

  /* Stop everything the bootloader started before the application takes over.
     HAL_RCC_DeInit() puts the clock tree back into its reset state (HSI), so the
     application starts exactly as it would after a power-on reset instead of
     inheriting the 72 MHz PLL of the bootloader. */
  Usart1_DeInit();
  HAL_RCC_DeInit();

  SysTick->CTRL = 0U;
  SysTick->LOAD = 0U;
  SysTick->VAL = 0U;

  Boot_NvicClearAll();

  __disable_irq();

  SCB->VTOR = BOOT_APP_ADDR;
  __DSB();
  __ISB();

  /* The stack switch and the branch must happen in one assembly block.  Calling
     __set_MSP() from C is NOT enough: the compiler restores the bootloader's own
     stack pointer in the function epilogue (pop {r4, pc}) right after the msr,
     so the application starts with sp = _estack + 0x10 and corrupts its stack
     on the first push.  Measured on hardware: the application's Reset_Handler
     ran with sp = 0x20005010 and died on a garbage branch a few instructions
     later, which looked exactly like "the jump hangs". */
  __asm volatile
  (
    "msr msp, %0     \n"
    "cpsie i         \n"
    "bx  %1          \n"
    :
    : "r" (stackPointer), "r" (resetHandler)
    : "memory"
  );

  for (;;)
  {
  }
}

void Boot_Reset(void)
{
  NVIC_SystemReset();
}

/* Diagnostics ---------------------------------------------------------------*/

void Boot_PrintResetCause(void)
{
  /* The F1 reset control register has no brown-out flag of its own; a supply
     dip shows up as a pin reset (or as POR + PIN), which is why the raw value
     is printed as well. */
  static const char *const causes[] =
  {
    "power-on", "reset pin", "software", "iwdg", "wwdg", "low power", "unknown"
  };
  static const char hexDigits[] = "0123456789ABCDEF";
  uint32_t flags = RCC->CSR;
  uint32_t index;
  char text[16];
  uint32_t i;

  if ((flags & RCC_CSR_PORRSTF) != 0U)
  {
    index = 0U;
  }
  else if ((flags & RCC_CSR_PINRSTF) != 0U)
  {
    index = 1U;
  }
  else if ((flags & RCC_CSR_SFTRSTF) != 0U)
  {
    index = 2U;
  }
  else if ((flags & RCC_CSR_IWDGRSTF) != 0U)
  {
    index = 3U;
  }
  else if ((flags & RCC_CSR_WWDGRSTF) != 0U)
  {
    index = 4U;
  }
  else if ((flags & RCC_CSR_LPWRRSTF) != 0U)
  {
    index = 5U;
  }
  else
  {
    index = 6U;
  }

  Usart1_Write((const uint8_t *)"reset cause: ", 13U);
  Usart1_Write((const uint8_t *)causes[index], (uint32_t)strlen(causes[index]));

  /* Raw register, so nothing is hidden by the decision above. */
  text[0] = ' ';
  text[1] = 'c';
  text[2] = 's';
  text[3] = 'r';
  text[4] = ' ';
  text[5] = '0';
  text[6] = 'x';
  for (i = 0U; i < 8U; i++)
  {
    text[7U + i] = hexDigits[(flags >> (28U - (4U * i))) & 0xFU];
  }

  Usart1_Write((const uint8_t *)text, 15U);
  Usart1_Write((const uint8_t *)"\r\n", 2U);

  RCC->CSR |= RCC_CSR_RMVF;
}

/* Bootloader update ---------------------------------------------------------*/

#define BOOT_FLASH_KEY1  0x45670123U
#define BOOT_FLASH_KEY2  0xCDEF89ABU

/**
  * @brief  Copy the staged image over the bootloader, running from SRAM.
  *
  * The .data section is loaded into RAM by the start-up code, so this function
  * keeps working after the flash page that holds it has been erased.  It must
  * not call anything: every function of this file lives in the flash it erases.
  */
__attribute__((section(".data"), noinline))
static void Boot_CopyStub(uint32_t source, uint32_t destination, uint32_t size)
{
  uint32_t page;
  uint32_t offset;
  uint16_t value;

  FLASH->KEYR = BOOT_FLASH_KEY1;
  FLASH->KEYR = BOOT_FLASH_KEY2;

  for (page = 0U; page < size; page += BOOT_FLAG_PAGE_SIZE)
  {
    FLASH->CR |= FLASH_CR_PER;
    FLASH->AR = destination + page;
    FLASH->CR |= FLASH_CR_STRT;
    while ((FLASH->SR & FLASH_SR_BSY) != 0U)
    {
    }
    FLASH->CR &= ~FLASH_CR_PER;
    FLASH->SR = FLASH_SR_EOP;
  }

  FLASH->CR |= FLASH_CR_PG;
  for (offset = 0U; offset < size; offset += 2U)
  {
    value = *(volatile const uint16_t *)(source + offset);
    *(volatile uint16_t *)(destination + offset) = value;
    while ((FLASH->SR & FLASH_SR_BSY) != 0U)
    {
    }
  }
  FLASH->CR &= ~FLASH_CR_PG;
  FLASH->SR = FLASH_SR_EOP;

  /* Compare what the flash really holds before declaring success. */
  for (offset = 0U; offset < size; offset += 2U)
  {
    if (*(volatile const uint16_t *)(destination + offset) !=
        *(volatile const uint16_t *)(source + offset))
    {
      FLASH->CR |= FLASH_CR_LOCK;
      for (;;)
      {
        /* Nothing sane is left to run: wait for the debugger. */
      }
    }
  }

  FLASH->CR |= FLASH_CR_LOCK;

  SCB->AIRCR = (0x05FAU << 16) | (1U << 2);
  for (;;)
  {
  }
}

int Boot_ApplyBootloaderUpdate(uint32_t size, uint32_t sourceCrc)
{
  uint32_t rounded;

  if ((size == 0U) || (size > BOOT_BOOTLOADER_SIZE) ||
      (Boot_RangeInFlash(BOOT_FLASH_BASE, BOOT_BOOTLOADER_SIZE) == 0))
  {
    return BOOT_ERR_SIZE;
  }

  /* The staged image must have been linked for the bootloader region. */
  if (Boot_VectorValidRange((const uint8_t *)BOOT_APP_ADDR, size,
                            BOOT_FLASH_BASE, BOOT_BOOTLOADER_SIZE) == 0)
  {
    return BOOT_ERR_PARAM;
  }

  if (Boot_ImageCrc(BOOT_APP_ADDR, size) != sourceCrc)
  {
    return BOOT_ERR_CRC;
  }

  rounded = (size + 1U) & ~1U;

  Usart1_DeInit();

  SysTick->CTRL = 0U;
  SysTick->LOAD = 0U;
  SysTick->VAL = 0U;

  Boot_NvicClearAll();
  __disable_irq();

  Boot_CopyStub(BOOT_APP_ADDR, BOOT_FLASH_BASE, rounded);

  /* Not reached: the stub resets the device. */
  return BOOT_ERR_FLASH;
}
