/**
  ******************************************************************************
  * @file    usbd_dual_cdc.c
  * @brief   Triple CDC-ACM composite class implementation (full speed only).
  *
  * Three independent CDC-ACM functions are described in one configuration with
  * Interface Association Descriptors so that a host enumerates them as three
  * separate COM ports which can be opened at the same time.
  ******************************************************************************
  */

/* Includes ------------------------------------------------------------------*/
#include "usbd_dual_cdc.h"
#include "usbd_ctlreq.h"

/* Private defines -----------------------------------------------------------*/
#define DUALCDC_CMD_IDLE          0xFFU

/* One CDC function block: IAD + communication interface + data interface. */
#define DUALCDC_FUNCTION_DESC(comm_if, data_if, in_ep, out_ep, cmd_ep, mps) \
  /* Interface Association Descriptor */ \
  0x08,                                 /* bLength */ \
  0x0B,                                 /* bDescriptorType: IAD */ \
  (comm_if),                            /* bFirstInterface */ \
  0x02,                                 /* bInterfaceCount */ \
  0x02,                                 /* bFunctionClass: CDC */ \
  0x02,                                 /* bFunctionSubClass: ACM */ \
  0x01,                                 /* bFunctionProtocol: AT commands */ \
  0x00,                                 /* iFunction */ \
  /* Communication class interface */ \
  0x09,                                 /* bLength */ \
  USB_DESC_TYPE_INTERFACE,              /* bDescriptorType */ \
  (comm_if),                            /* bInterfaceNumber */ \
  0x00,                                 /* bAlternateSetting */ \
  0x01,                                 /* bNumEndpoints */ \
  0x02,                                 /* bInterfaceClass: CDC */ \
  0x02,                                 /* bInterfaceSubClass: ACM */ \
  0x01,                                 /* bInterfaceProtocol: AT commands */ \
  0x00,                                 /* iInterface */ \
  /* Header functional descriptor */ \
  0x05, 0x24, 0x00, 0x10, 0x01, \
  /* Call management functional descriptor */ \
  0x05, 0x24, 0x01, 0x00, 0x01, \
  /* ACM functional descriptor */ \
  0x04, 0x24, 0x02, 0x02, \
  /* Union functional descriptor: comm -> data */ \
  0x05, 0x24, 0x06, (comm_if), (data_if), \
  /* Notification (interrupt IN) endpoint */ \
  0x07,                                 /* bLength */ \
  USB_DESC_TYPE_ENDPOINT,               /* bDescriptorType */ \
  (cmd_ep),                             /* bEndpointAddress */ \
  0x03,                                 /* bmAttributes: interrupt */ \
  0x08, 0x00,                           /* wMaxPacketSize */ \
  0x10,                                 /* bInterval */ \
  /* Data class interface */ \
  0x09,                                 /* bLength */ \
  USB_DESC_TYPE_INTERFACE,              /* bDescriptorType */ \
  (data_if),                            /* bInterfaceNumber */ \
  0x00,                                 /* bAlternateSetting */ \
  0x02,                                 /* bNumEndpoints */ \
  0x0A,                                 /* bInterfaceClass: CDC data */ \
  0x00,                                 /* bInterfaceSubClass */ \
  0x00,                                 /* bInterfaceProtocol */ \
  0x00,                                 /* iInterface */ \
  /* Bulk OUT endpoint */ \
  0x07,                                 /* bLength */ \
  USB_DESC_TYPE_ENDPOINT,               /* bDescriptorType */ \
  (out_ep),                             /* bEndpointAddress */ \
  0x02,                                 /* bmAttributes: bulk */ \
  LOBYTE(mps), HIBYTE(mps),             /* wMaxPacketSize */ \
  0x00,                                 /* bInterval */ \
  /* Bulk IN endpoint */ \
  0x07,                                 /* bLength */ \
  USB_DESC_TYPE_ENDPOINT,               /* bDescriptorType */ \
  (in_ep),                              /* bEndpointAddress */ \
  0x02,                                 /* bmAttributes: bulk */ \
  LOBYTE(mps), HIBYTE(mps),             /* wMaxPacketSize */ \
  0x00                                  /* bInterval */

/* Private variables ---------------------------------------------------------*/
/** Configuration descriptor: 9 + 3 * 66 = 207 bytes. */
__ALIGN_BEGIN static uint8_t USBD_DUALCDC_CfgFSDesc[] __ALIGN_END =
{
  /* Configuration descriptor (9 bytes) */
  0x09,                                 /* bLength */
  USB_DESC_TYPE_CONFIGURATION,          /* bDescriptorType */
  LOBYTE(DUALCDC_CONFIG_DESC_SIZ),      /* wTotalLength */
  HIBYTE(DUALCDC_CONFIG_DESC_SIZ),
  0x06,                                 /* bNumInterfaces: 6 */
  0x01,                                 /* bConfigurationValue */
  0x00,                                 /* iConfiguration */
  0x80,                                 /* bmAttributes: bus powered */
  0x32,                                 /* bMaxPower: 100 mA */

  /* Function 0: communication interface 0 + data interface 1 (port 0) */
  DUALCDC_FUNCTION_DESC(0x00, 0x01,
                        DUALCDC_CDC_IN_EP, DUALCDC_CDC_OUT_EP, DUALCDC_CDC_CMD_EP,
                        DUALCDC_DATA_FS_MAX_PACKET_SIZE),

  /* Function 1: communication interface 2 + data interface 3 (port 1) */
  DUALCDC_FUNCTION_DESC(0x02, 0x03,
                        DUALCDC_CDC2_IN_EP, DUALCDC_CDC2_OUT_EP, DUALCDC_CDC2_CMD_EP,
                        DUALCDC_DATA_FS_MAX_PACKET_SIZE),

  /* Function 2: communication interface 4 + data interface 5 (port 2, shell) */
  DUALCDC_FUNCTION_DESC(0x04, 0x05,
                        DUALCDC_CDC3_IN_EP, DUALCDC_CDC3_OUT_EP, DUALCDC_CDC3_CMD_EP,
                        DUALCDC_DATA_SHELL_MAX_PACKET_SIZE)
};

_Static_assert(sizeof(USBD_DUALCDC_CfgFSDesc) == DUALCDC_CONFIG_DESC_SIZ,
               "triple CDC configuration descriptor size mismatch");

/* Endpoint and packet size tables, indexed by port. */
static const uint8_t USBD_DUALCDC_InEp[DUALCDC_PORT_COUNT] =
{
  DUALCDC_CDC_IN_EP, DUALCDC_CDC2_IN_EP, DUALCDC_CDC3_IN_EP
};
static const uint8_t USBD_DUALCDC_OutEp[DUALCDC_PORT_COUNT] =
{
  DUALCDC_CDC_OUT_EP, DUALCDC_CDC2_OUT_EP, DUALCDC_CDC3_OUT_EP
};
static const uint8_t USBD_DUALCDC_CmdEp[DUALCDC_PORT_COUNT] =
{
  DUALCDC_CDC_CMD_EP, DUALCDC_CDC2_CMD_EP, DUALCDC_CDC3_CMD_EP
};
static const uint16_t USBD_DUALCDC_DataMps[DUALCDC_PORT_COUNT] =
{
  DUALCDC_DATA_FS_MAX_PACKET_SIZE,
  DUALCDC_DATA_FS_MAX_PACKET_SIZE,
  DUALCDC_DATA_SHELL_MAX_PACKET_SIZE
};

/* USB device qualifier descriptor (full speed only) */
__ALIGN_BEGIN static uint8_t USBD_DUALCDC_DeviceQualifierDesc[USB_LEN_DEV_QUALIFIER_DESC] __ALIGN_END =
{
  USB_LEN_DEV_QUALIFIER_DESC,
  USB_DESC_TYPE_DEVICE_QUALIFIER,
  0x00,
  0x02,
  0x00,
  0x00,
  0x00,
  0x40,
  0x01,
  0x00
};

/* Application interface */
static USBD_DUALCDC_ItfTypeDef *USBD_DUALCDC_fops = NULL;

/* Private function prototypes -----------------------------------------------*/
static uint8_t USBD_DUALCDC_Init(USBD_HandleTypeDef *pdev, uint8_t cfgidx);
static uint8_t USBD_DUALCDC_DeInit(USBD_HandleTypeDef *pdev, uint8_t cfgidx);
static uint8_t USBD_DUALCDC_Setup(USBD_HandleTypeDef *pdev, USBD_SetupReqTypedef *req);
static uint8_t USBD_DUALCDC_EP0_RxReady(USBD_HandleTypeDef *pdev);
static uint8_t USBD_DUALCDC_DataIn(USBD_HandleTypeDef *pdev, uint8_t epnum);
static uint8_t USBD_DUALCDC_DataOut(USBD_HandleTypeDef *pdev, uint8_t epnum);
static uint8_t *USBD_DUALCDC_GetFSCfgDesc(uint16_t *length);
static uint8_t *USBD_DUALCDC_GetHSCfgDesc(uint16_t *length);
static uint8_t *USBD_DUALCDC_GetOtherSpeedCfgDesc(uint16_t *length);
static uint8_t *USBD_DUALCDC_GetDeviceQualifierDescriptor(uint16_t *length);

/* Class callback table */
USBD_ClassTypeDef USBD_DUALCDC =
{
  USBD_DUALCDC_Init,
  USBD_DUALCDC_DeInit,
  USBD_DUALCDC_Setup,
  NULL,                                 /* EP0_TxSent */
  USBD_DUALCDC_EP0_RxReady,
  USBD_DUALCDC_DataIn,
  USBD_DUALCDC_DataOut,
  NULL,                                 /* SOF */
  NULL,                                 /* IsoINIncomplete */
  NULL,                                 /* IsoOUTIncomplete */
  USBD_DUALCDC_GetHSCfgDesc,
  USBD_DUALCDC_GetFSCfgDesc,
  USBD_DUALCDC_GetOtherSpeedCfgDesc,
  USBD_DUALCDC_GetDeviceQualifierDescriptor
};

/* Private functions ---------------------------------------------------------*/

/**
  * @brief  Convert a bare endpoint number (as delivered by the LL callbacks)
  *         into a port index, or DUALCDC_PORT_COUNT when it is not ours.
  */
static uint8_t USBD_DUALCDC_PortFromInEp(uint8_t epnum)
{
  uint8_t i;

  for (i = 0U; i < DUALCDC_PORT_COUNT; i++)
  {
    if ((USBD_DUALCDC_InEp[i] & 0x0FU) == epnum)
    {
      return i;
    }
  }
  return (uint8_t)DUALCDC_PORT_COUNT;
}

/**
  * @brief  Same lookup for the OUT direction.
  * @note   The endpoints must be resolved per direction: the F1 USB core has
  *         one register per endpoint *number*, so this build reuses number 3
  *         for the shell bulk OUT (0x03) and, unavoidably, for a bulk IN of
  *         another port (0x83).  Matching both tables at once made the shell
  *         data be delivered to port 1 and left 0x03 unanswered.
  */
static uint8_t USBD_DUALCDC_PortFromOutEp(uint8_t epnum)
{
  uint8_t i;

  for (i = 0U; i < DUALCDC_PORT_COUNT; i++)
  {
    if ((USBD_DUALCDC_OutEp[i] & 0x0FU) == epnum)
    {
      return i;
    }
  }
  return (uint8_t)DUALCDC_PORT_COUNT;
}

/**
  * @brief  Open one bulk IN/OUT pair plus one interrupt IN endpoint.
  */
static void USBD_DUALCDC_OpenPort(USBD_HandleTypeDef *pdev, uint8_t port)
{
  uint8_t in_ep = USBD_DUALCDC_InEp[port];
  uint8_t out_ep = USBD_DUALCDC_OutEp[port];
  uint8_t cmd_ep = USBD_DUALCDC_CmdEp[port];

  USBD_LL_OpenEP(pdev, in_ep, USBD_EP_TYPE_BULK, USBD_DUALCDC_DataMps[port]);
  pdev->ep_in[in_ep & 0x0FU].is_used = 1U;

  USBD_LL_OpenEP(pdev, out_ep, USBD_EP_TYPE_BULK, USBD_DUALCDC_DataMps[port]);
  pdev->ep_out[out_ep & 0x0FU].is_used = 1U;

  USBD_LL_OpenEP(pdev, cmd_ep, USBD_EP_TYPE_INTR, DUALCDC_CMD_PACKET_SIZE);
  pdev->ep_in[cmd_ep & 0x0FU].is_used = 1U;
}

/**
  * @brief  Initialize the triple CDC interface.
  */
static uint8_t USBD_DUALCDC_Init(USBD_HandleTypeDef *pdev, uint8_t cfgidx)
{
  USBD_DUALCDC_HandleTypeDef *hcdc;
  uint8_t i;
  (void)cfgidx;

  for (i = 0U; i < DUALCDC_PORT_COUNT; i++)
  {
    USBD_DUALCDC_OpenPort(pdev, i);
  }

  pdev->pClassData = USBD_malloc(sizeof(USBD_DUALCDC_HandleTypeDef));
  if (pdev->pClassData == NULL)
  {
    return USBD_FAIL;
  }

  hcdc = (USBD_DUALCDC_HandleTypeDef *)pdev->pClassData;
  for (i = 0U; i < DUALCDC_PORT_COUNT; i++)
  {
    hcdc->port[i].CmdOpCode = DUALCDC_CMD_IDLE;
    hcdc->port[i].CmdLength = 0U;
    hcdc->port[i].RxBuffer = NULL;
    hcdc->port[i].TxBuffer = NULL;
    hcdc->port[i].RxLength = 0U;
    hcdc->port[i].TxLength = 0U;
    hcdc->port[i].TxState = 0U;
    hcdc->port[i].RxState = 0U;
  }

  if (USBD_DUALCDC_fops != NULL)
  {
    for (i = 0U; i < DUALCDC_PORT_COUNT; i++)
    {
      (void)USBD_DUALCDC_fops->Init(i);
    }
  }

  /* Arm the OUT endpoints (RxBuffer was set by fops->Init). */
  for (i = 0U; i < DUALCDC_PORT_COUNT; i++)
  {
    USBD_LL_PrepareReceive(pdev, USBD_DUALCDC_OutEp[i],
                           hcdc->port[i].RxBuffer, USBD_DUALCDC_DataMps[i]);
  }

  return USBD_OK;
}

/**
  * @brief  De-initialize the triple CDC interface.
  */
static uint8_t USBD_DUALCDC_DeInit(USBD_HandleTypeDef *pdev, uint8_t cfgidx)
{
  uint8_t i;
  (void)cfgidx;

  for (i = 0U; i < DUALCDC_PORT_COUNT; i++)
  {
    USBD_LL_CloseEP(pdev, USBD_DUALCDC_InEp[i]);
    pdev->ep_in[USBD_DUALCDC_InEp[i] & 0x0FU].is_used = 0U;

    USBD_LL_CloseEP(pdev, USBD_DUALCDC_OutEp[i]);
    pdev->ep_out[USBD_DUALCDC_OutEp[i] & 0x0FU].is_used = 0U;

    USBD_LL_CloseEP(pdev, USBD_DUALCDC_CmdEp[i]);
    pdev->ep_in[USBD_DUALCDC_CmdEp[i] & 0x0FU].is_used = 0U;
  }

  if (USBD_DUALCDC_fops != NULL)
  {
    for (i = 0U; i < DUALCDC_PORT_COUNT; i++)
    {
      (void)USBD_DUALCDC_fops->DeInit(i);
    }
  }

  if (pdev->pClassData != NULL)
  {
    USBD_free(pdev->pClassData);
    pdev->pClassData = NULL;
  }

  return USBD_OK;
}

/**
  * @brief  Handle control requests for the six interfaces of the composite.
  */
static uint8_t USBD_DUALCDC_Setup(USBD_HandleTypeDef *pdev, USBD_SetupReqTypedef *req)
{
  USBD_DUALCDC_HandleTypeDef *hcdc = (USBD_DUALCDC_HandleTypeDef *)pdev->pClassData;
  static uint8_t ifalt = 0U;
  static uint8_t status_info[2] = {0U, 0U};
  uint8_t port;

  if (hcdc == NULL)
  {
    USBD_CtlError(pdev, req);
    return USBD_FAIL;
  }

  if ((req->bmRequest & USB_REQ_TYPE_MASK) == USB_REQ_TYPE_CLASS)
  {
    /* Communication interface 0/2/4 maps to port 0/1/2 (data interfaces
       1/3/5 map to the same port). */
    port = (uint8_t)(LOBYTE(req->wIndex) >> 1);
    if (port >= DUALCDC_PORT_COUNT)
    {
      USBD_CtlError(pdev, req);
      return USBD_FAIL;
    }

    switch (req->bRequest)
    {
      case DUALCDC_REQ_SET_LINE_CODING:
        hcdc->port[port].CmdOpCode = req->bRequest;
        hcdc->port[port].CmdLength = req->wLength;
        USBD_CtlPrepareRx(pdev, hcdc->data, req->wLength);
        break;

      case DUALCDC_REQ_GET_LINE_CODING:
        if (USBD_DUALCDC_fops == NULL)
        {
          USBD_CtlError(pdev, req);
          return USBD_FAIL;
        }
        if (USBD_DUALCDC_fops->Control(port, req->bRequest, hcdc->data,
                                       req->wLength) != USBD_OK)
        {
          USBD_CtlError(pdev, req);
          return USBD_FAIL;
        }
        USBD_CtlSendData(pdev, hcdc->data, req->wLength);
        break;

      case DUALCDC_REQ_SET_CONTROL_LINE_STATE:
        if (USBD_DUALCDC_fops != NULL)
        {
          (void)USBD_DUALCDC_fops->Control(port, req->bRequest,
                                           (uint8_t *)req, 0U);
        }
        break;

      case DUALCDC_REQ_SEND_BREAK:
        if (USBD_DUALCDC_fops != NULL)
        {
          (void)USBD_DUALCDC_fops->Control(port, req->bRequest,
                                           (uint8_t *)req, 0U);
        }
        break;

      default:
        USBD_CtlError(pdev, req);
        return USBD_FAIL;
    }
    return USBD_OK;
  }

  /* Standard interface requests. */
  switch (req->bRequest)
  {
    case USB_REQ_GET_STATUS:
      if (pdev->dev_state == USBD_STATE_CONFIGURED)
      {
        USBD_CtlSendData(pdev, status_info, MIN(req->wLength, 2U));
      }
      else
      {
        USBD_CtlError(pdev, req);
        return USBD_FAIL;
      }
      break;

    case USB_REQ_GET_INTERFACE:
      if (pdev->dev_state == USBD_STATE_CONFIGURED)
      {
        USBD_CtlSendData(pdev, &ifalt, MIN(req->wLength, 1U));
      }
      else
      {
        USBD_CtlError(pdev, req);
        return USBD_FAIL;
      }
      break;

    case USB_REQ_SET_INTERFACE:
      if ((pdev->dev_state == USBD_STATE_CONFIGURED) &&
          (LOBYTE(req->wIndex) < USBD_MAX_NUM_INTERFACES))
      {
        ifalt = HIBYTE(req->wValue);
      }
      else
      {
        USBD_CtlError(pdev, req);
        return USBD_FAIL;
      }
      break;

    default:
      USBD_CtlError(pdev, req);
      return USBD_FAIL;
  }

  return USBD_OK;
}

/**
  * @brief  Data stage of a SET_LINE_CODING request has been received.
  */
static uint8_t USBD_DUALCDC_EP0_RxReady(USBD_HandleTypeDef *pdev)
{
  USBD_DUALCDC_HandleTypeDef *hcdc = (USBD_DUALCDC_HandleTypeDef *)pdev->pClassData;
  uint8_t i;

  if (hcdc == NULL || USBD_DUALCDC_fops == NULL)
  {
    return USBD_OK;
  }

  for (i = 0U; i < DUALCDC_PORT_COUNT; i++)
  {
    if (hcdc->port[i].CmdOpCode != DUALCDC_CMD_IDLE)
    {
      (void)USBD_DUALCDC_fops->Control(i, (uint8_t)hcdc->port[i].CmdOpCode,
                                       hcdc->data,
                                       (uint16_t)hcdc->port[i].CmdLength);
      hcdc->port[i].CmdOpCode = DUALCDC_CMD_IDLE;
    }
  }

  return USBD_OK;
}

/**
  * @brief  IN transfer complete on a bulk endpoint.
  */
static uint8_t USBD_DUALCDC_DataIn(USBD_HandleTypeDef *pdev, uint8_t epnum)
{
  USBD_DUALCDC_HandleTypeDef *hcdc = (USBD_DUALCDC_HandleTypeDef *)pdev->pClassData;
  uint8_t port;

  if (hcdc == NULL)
  {
    return USBD_OK;
  }

  port = USBD_DUALCDC_PortFromInEp(epnum);
  if (port >= DUALCDC_PORT_COUNT)
  {
    return USBD_OK;
  }

  if (pdev->ep_in[epnum].total_length > 0U)
  {
    if ((pdev->ep_in[epnum].total_length % USBD_DUALCDC_DataMps[port]) == 0U)
    {
      /* Transfer length was a multiple of the packet size: terminate with a
         zero length packet, TxState stays busy until that packet completes. */
      pdev->ep_in[epnum].total_length = 0U;
      USBD_LL_Transmit(pdev, epnum, NULL, 0U);
      return USBD_OK;
    }
    pdev->ep_in[epnum].total_length = 0U;
  }

  hcdc->port[port].TxState = 0U;
  return USBD_OK;
}

/**
  * @brief  OUT data received on a bulk endpoint.
  */
static uint8_t USBD_DUALCDC_DataOut(USBD_HandleTypeDef *pdev, uint8_t epnum)
{
  USBD_DUALCDC_HandleTypeDef *hcdc = (USBD_DUALCDC_HandleTypeDef *)pdev->pClassData;
  uint8_t port;

  if (hcdc == NULL)
  {
    return USBD_OK;
  }

  port = USBD_DUALCDC_PortFromOutEp(epnum);
  if (port >= DUALCDC_PORT_COUNT)
  {
    return USBD_OK;
  }

  hcdc->port[port].RxLength = USBD_LL_GetRxDataSize(pdev, epnum);

  if (USBD_DUALCDC_fops != NULL && USBD_DUALCDC_fops->Receive != NULL)
  {
    (void)USBD_DUALCDC_fops->Receive(port, hcdc->port[port].RxBuffer,
                                     &hcdc->port[port].RxLength);
  }

  return USBD_OK;
}

/* Descriptors ---------------------------------------------------------------*/

static uint8_t *USBD_DUALCDC_GetFSCfgDesc(uint16_t *length)
{
  *length = (uint16_t)sizeof(USBD_DUALCDC_CfgFSDesc);
  return USBD_DUALCDC_CfgFSDesc;
}

static uint8_t *USBD_DUALCDC_GetHSCfgDesc(uint16_t *length)
{
  *length = (uint16_t)sizeof(USBD_DUALCDC_CfgFSDesc);
  return USBD_DUALCDC_CfgFSDesc;
}

static uint8_t *USBD_DUALCDC_GetOtherSpeedCfgDesc(uint16_t *length)
{
  *length = (uint16_t)sizeof(USBD_DUALCDC_CfgFSDesc);
  return USBD_DUALCDC_CfgFSDesc;
}

static uint8_t *USBD_DUALCDC_GetDeviceQualifierDescriptor(uint16_t *length)
{
  *length = (uint16_t)sizeof(USBD_DUALCDC_DeviceQualifierDesc);
  return USBD_DUALCDC_DeviceQualifierDesc;
}

/* Public functions ----------------------------------------------------------*/

uint8_t USBD_DUALCDC_RegisterInterface(USBD_HandleTypeDef *pdev,
                                       USBD_DUALCDC_ItfTypeDef *fops)
{
  if (fops == NULL)
  {
    return USBD_FAIL;
  }
  USBD_DUALCDC_fops = fops;
  pdev->pUserData = fops;
  return USBD_OK;
}

uint8_t USBD_DUALCDC_SetTxBuffer(USBD_HandleTypeDef *pdev,
                                 uint8_t *pbuf, uint16_t len, uint8_t port)
{
  USBD_DUALCDC_HandleTypeDef *hcdc = (USBD_DUALCDC_HandleTypeDef *)pdev->pClassData;

  if ((hcdc == NULL) || (port >= DUALCDC_PORT_COUNT))
  {
    return USBD_FAIL;
  }
  hcdc->port[port].TxBuffer = pbuf;
  hcdc->port[port].TxLength = len;
  return USBD_OK;
}

uint8_t USBD_DUALCDC_SetRxBuffer(USBD_HandleTypeDef *pdev,
                                 uint8_t *pbuf, uint8_t port)
{
  USBD_DUALCDC_HandleTypeDef *hcdc = (USBD_DUALCDC_HandleTypeDef *)pdev->pClassData;

  if ((hcdc == NULL) || (port >= DUALCDC_PORT_COUNT))
  {
    return USBD_FAIL;
  }
  hcdc->port[port].RxBuffer = pbuf;
  hcdc->port[port].RxState = 0U;
  return USBD_OK;
}

uint8_t USBD_DUALCDC_TransmitPacket(USBD_HandleTypeDef *pdev, uint8_t port)
{
  USBD_DUALCDC_HandleTypeDef *hcdc = (USBD_DUALCDC_HandleTypeDef *)pdev->pClassData;
  USBD_DUALCDC_PortHandleTypeDef *h;
  uint8_t ep;

  if ((hcdc == NULL) || (port >= DUALCDC_PORT_COUNT))
  {
    return USBD_FAIL;
  }

  h = &hcdc->port[port];
  if (h->TxState != 0U)
  {
    return USBD_BUSY;
  }

  h->TxState = 1U;
  ep = USBD_DUALCDC_InEp[port];
  pdev->ep_in[ep & 0x0FU].total_length = h->TxLength;
  (void)USBD_LL_Transmit(pdev, ep, h->TxBuffer, (uint16_t)h->TxLength);

  return USBD_OK;
}

uint8_t USBD_DUALCDC_ReceivePacket(USBD_HandleTypeDef *pdev, uint8_t port)
{
  USBD_DUALCDC_HandleTypeDef *hcdc = (USBD_DUALCDC_HandleTypeDef *)pdev->pClassData;
  USBD_DUALCDC_PortHandleTypeDef *h;

  if ((hcdc == NULL) || (port >= DUALCDC_PORT_COUNT))
  {
    return USBD_FAIL;
  }

  h = &hcdc->port[port];
  (void)USBD_LL_PrepareReceive(pdev, USBD_DUALCDC_OutEp[port],
                               h->RxBuffer, USBD_DUALCDC_DataMps[port]);
  return USBD_OK;
}

uint8_t USBD_DUALCDC_Transmit_FS(USBD_HandleTypeDef *pdev,
                                 uint8_t *Buf, uint16_t Len, uint8_t port)
{
  USBD_DUALCDC_HandleTypeDef *hcdc = (USBD_DUALCDC_HandleTypeDef *)pdev->pClassData;

  if ((hcdc == NULL) || (port >= DUALCDC_PORT_COUNT))
  {
    return USBD_FAIL;
  }

  /* Do not touch TxBuffer while a transfer is in flight: the USB peripheral
     reads directly from that memory. */
  if (hcdc->port[port].TxState != 0U)
  {
    return USBD_BUSY;
  }

  if (USBD_DUALCDC_SetTxBuffer(pdev, Buf, Len, port) != USBD_OK)
  {
    return USBD_FAIL;
  }
  return USBD_DUALCDC_TransmitPacket(pdev, port);
}

uint8_t USBD_DUALCDC_TxReady(USBD_HandleTypeDef *pdev, uint8_t port)
{
  USBD_DUALCDC_HandleTypeDef *hcdc = (USBD_DUALCDC_HandleTypeDef *)pdev->pClassData;

  if ((hcdc == NULL) || (port >= DUALCDC_PORT_COUNT))
  {
    return 0U;
  }
  return (hcdc->port[port].TxState == 0U) ? 1U : 0U;
}
