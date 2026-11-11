/* USER CODE BEGIN Header */
/**
  ******************************************************************************
  * @file           : main.c
  * @brief          : Main program body
  ******************************************************************************
  * @attention
  *
  * Copyright (c) 2026 STMicroelectronics.
  * All rights reserved.
  *
  * This software is licensed under terms that can be found in the LICENSE file
  * in the root directory of this software component.
  * If no LICENSE file comes with this software, it is provided AS-IS.
  *
  ******************************************************************************
  */
/* USER CODE END Header */
/* Includes ------------------------------------------------------------------*/
#include "main.h"
#include "usart.h"
#include "gpio.h"

/* Private includes ----------------------------------------------------------*/
/* USER CODE BEGIN Includes */
#include "boot.h"
#include "shell_port.h"
/* USER CODE END Includes */

/* Private typedef -----------------------------------------------------------*/
/* USER CODE BEGIN PTD */

/* USER CODE END PTD */

/* Private define ------------------------------------------------------------*/
/* USER CODE BEGIN PD */
/* Boot delay: the user has this many seconds to press any key and stay in the
   command line instead of starting the application. */
#define BOOT_COUNTDOWN_SECONDS   3U
#define BOOT_KEY_POLL_MS         10U

/* Write a string literal to the console (its length is known at compile time,
   which avoids printing strlen() calls in the flash critical bootloader). */
#define BOOT_PUTS(text)  Shell_WriteRaw((const uint8_t *)(text), \
                                        (uint32_t)(sizeof(text) - 1U))
/* USER CODE END PD */

/* Private macro -------------------------------------------------------------*/
/* USER CODE BEGIN PM */

/* USER CODE END PM */

/* Private variables ---------------------------------------------------------*/

/* USER CODE BEGIN PV */

/* USER CODE END PV */

/* Private function prototypes -----------------------------------------------*/
void SystemClock_Config(void);
/* USER CODE BEGIN PFP */
static int Boot_KeyPressed(uint32_t milliseconds);
static void Boot_StartConsole(void);
/* USER CODE END PFP */

/* Private user code ---------------------------------------------------------*/
/* USER CODE BEGIN 0 */

/**
  * @brief  Wait for a key press, giving up after "milliseconds".
  * @retval 1 when a byte is waiting in the console ring, 0 on timeout.
  */
static int Boot_KeyPressed(uint32_t milliseconds)
{
  uint32_t elapsed = 0U;

  while (elapsed < milliseconds)
  {
    if (Shell_HasByte() != 0)
    {
      return 1;
    }

    HAL_Delay(BOOT_KEY_POLL_MS);
    elapsed += BOOT_KEY_POLL_MS;
  }

  return (Shell_HasByte() != 0) ? 1 : 0;
}

/**
  * @brief  Enter the letter-shell console and never return.
  */
static void Boot_StartConsole(void)
{
  /* shellInit() prints the first prompt itself, so the hint has to be written
     before it to appear above the prompt. */
  BOOT_PUTS("console ready, 'help' lists the commands\r\n");
  Shell_Init();
  Shell_Loop();
}

/* USER CODE END 0 */

/**
  * @brief  The application entry point.
  * @retval int
  */
int main(void)
{
  /* USER CODE BEGIN 1 */

  /* USER CODE END 1 */

  /* MCU Configuration--------------------------------------------------------*/

  /* Reset of all peripherals, Initializes the Flash interface and the Systick. */
  HAL_Init();

  /* USER CODE BEGIN Init */

  /* USER CODE END Init */

  /* Configure the system clock */
  SystemClock_Config();

  /* USER CODE BEGIN SysInit */

  /* USER CODE END SysInit */

  /* Initialize all configured peripherals */
  MX_GPIO_Init();
  /* USER CODE BEGIN 2 */

  /* Reception has to run during the countdown, otherwise a key pressed before
     the console is initialised would be lost. */
  Shell_RxInit();

  /* Always refreshed by the compiler, unlike the letter-shell banner which only
     reflects the build date of shell.c.  This is the line to look at to know
     which bootloader image is really running. */
  BOOT_PUTS("\r\nbootloader build " __DATE__ " " __TIME__ "\r\n");

  /* Tells whether the device really restarted after a jump and why. */
  Boot_PrintResetCause();

  BOOT_PUTS("\r\nbootloader: press any key within ");

  {
    static const char digits[BOOT_COUNTDOWN_SECONDS] = {'3', '2', '1'};
    uint32_t index;
    int aborted = 0;

    for (index = 0U; index < BOOT_COUNTDOWN_SECONDS; index++)
    {
      /* Overwrite the previous digit instead of appending a new one, so the
         countdown stays at one position on the terminal ("\b" moves the cursor
         back over the digit that is already there). */
      if (index > 0U)
      {
        BOOT_PUTS("\b");
      }

      Shell_WriteRaw((const uint8_t *)&digits[index], 1U);

      if (Boot_KeyPressed(1000U) != 0)
      {
        aborted = 1;
        break;
      }
    }

    BOOT_PUTS("\r\n");

    if (aborted == 0)
    {
      BootFlag flag;
      int hasFlag;

      if (Boot_ImageValid(BOOT_APP_ADDR) == 0)
      {
        BOOT_PUTS("no application image found, staying in the bootloader\r\n");
      }
      else
      {
        /* The vector table check alone cannot notice a flash cell that flipped
           later on, so the recorded image checksum is verified as well.  A
           mismatch keeps the device in the bootloader instead of running a
           half valid image. */
        uint32_t imageSize = BOOT_APP_SIZE;

        hasFlag = Boot_ReadFlag(&flag);

        if ((hasFlag != 0) && (flag.image_size != 0U))
        {
          imageSize = flag.image_size;
        }

        if ((hasFlag != 0) && (flag.image_size != 0U) &&
            (Boot_ImageCrc(BOOT_APP_ADDR, flag.image_size) != flag.image_crc))
        {
          BOOT_PUTS("the application does not match the recorded checksum,\r\n"
                    "staying in the bootloader\r\n");
          BOOT_PUTS("send the image again with 'ymodem-ota k'\r\n");
        }
        else if (Boot_RangeInFlash(BOOT_APP_ADDR, imageSize) == 0)
        {
          /* An image that reaches past the flash this device really reports
             would run from memory that does not exist. */
          BOOT_PUTS("the application does not fit in this device's flash,\r\n"
                    "staying in the bootloader\r\n");
          BOOT_PUTS("run 'bootinfo' to see the reported flash size\r\n");
        }
        else
        {
          BOOT_PUTS("starting application ...\r\n");
          Boot_JumpToApp();

          /* Boot_JumpToApp() only returns when the image turned out to be
             unusable, which the checks above already excluded. */
          BOOT_PUTS("the jump to the application was refused\r\n");
        }
      }
    }
    else
    {
      BOOT_PUTS("command line requested\r\n");
    }
  }

  /* USER CODE END 2 */

  /* Infinite loop */
  /* USER CODE BEGIN WHILE */
  Boot_StartConsole();

  while (1)
  {
    /* USER CODE END WHILE */

    /* USER CODE BEGIN 3 */
  }
  /* USER CODE END 3 */
}

/**
  * @brief System Clock Configuration
  * @retval None
  */
void SystemClock_Config(void)
{
  RCC_OscInitTypeDef RCC_OscInitStruct = {0};
  RCC_ClkInitTypeDef RCC_ClkInitStruct = {0};

  /** Initializes the RCC Oscillators according to the specified parameters
  * in the RCC_OscInitTypeDef structure.
  */
  RCC_OscInitStruct.OscillatorType = RCC_OSCILLATORTYPE_HSE;
  RCC_OscInitStruct.HSEState = RCC_HSE_ON;
  RCC_OscInitStruct.HSEPredivValue = RCC_HSE_PREDIV_DIV1;
  RCC_OscInitStruct.HSIState = RCC_HSI_ON;
  RCC_OscInitStruct.PLL.PLLState = RCC_PLL_ON;
  RCC_OscInitStruct.PLL.PLLSource = RCC_PLLSOURCE_HSE;
  RCC_OscInitStruct.PLL.PLLMUL = RCC_PLL_MUL9;
  if (HAL_RCC_OscConfig(&RCC_OscInitStruct) != HAL_OK)
  {
    Error_Handler();
  }

  /** Initializes the CPU, AHB and APB buses clocks
  */
  RCC_ClkInitStruct.ClockType = RCC_CLOCKTYPE_HCLK|RCC_CLOCKTYPE_SYSCLK
                              |RCC_CLOCKTYPE_PCLK1|RCC_CLOCKTYPE_PCLK2;
  RCC_ClkInitStruct.SYSCLKSource = RCC_SYSCLKSOURCE_PLLCLK;
  RCC_ClkInitStruct.AHBCLKDivider = RCC_SYSCLK_DIV1;
  RCC_ClkInitStruct.APB1CLKDivider = RCC_HCLK_DIV2;
  RCC_ClkInitStruct.APB2CLKDivider = RCC_HCLK_DIV1;

  if (HAL_RCC_ClockConfig(&RCC_ClkInitStruct, FLASH_LATENCY_2) != HAL_OK)
  {
    Error_Handler();
  }
}

/* USER CODE BEGIN 4 */

/* USER CODE END 4 */

/**
  * @brief  This function is executed in case of error occurrence.
  * @retval None
  */
void Error_Handler(void)
{
  /* USER CODE BEGIN Error_Handler_Debug */
  /* User can add his own implementation to report the HAL error return state */
  __disable_irq();
  while (1)
  {
  }
  /* USER CODE END Error_Handler_Debug */
}

#ifdef  USE_FULL_ASSERT
/**
  * @brief  Reports the name of the source file and the source line number
  *         where the assert_param error has occurred.
  * @param  file: pointer to the source file name
  * @param  line: assert_param error line source number
  * @retval None
  */
void assert_failed(uint8_t *file, uint32_t line)
{
  /* USER CODE BEGIN 6 */
  /* User can add his own implementation to report the file name and line number,
     ex: printf("Wrong parameters value: file %s on line %d\r\n", file, line) */
  /* USER CODE END 6 */
}
#endif /* USE_FULL_ASSERT */
