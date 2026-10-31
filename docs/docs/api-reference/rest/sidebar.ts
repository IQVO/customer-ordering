import type { SidebarsConfig } from "@docusaurus/plugin-content-docs";

const sidebar: SidebarsConfig = {
  apisidebar: [
    {
      type: "doc",
      id: "api-reference/rest/customer-ordering-api",
    },
    {
      type: "category",
      label: "Checkout",
      link: {
        type: "doc",
        id: "api-reference/rest/checkout",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/place-order",
          label: "Place a customer order, promising and confirming every shipment group",
          className: "api-method post",
        },
      ],
    },
    {
      type: "category",
      label: "Orders",
      link: {
        type: "doc",
        id: "api-reference/rest/orders",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/list-orders-by-shopper",
          label: "List a shopper's order history, newest first",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/get-order",
          label: "Get a customer order with its status and the promise per group",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/get-order-tracking",
          label: "Get the order's tracking view per shipment group and package",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/cancel-order",
          label: "Request the cancellation of every group that is not yet locked",
          className: "api-method post",
        },
      ],
    },
    {
      type: "category",
      label: "Ship-To",
      link: {
        type: "doc",
        id: "api-reference/rest/ship-to",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/get-ship-to",
          label: "Get the ship-to address of an order (restricted, label printing only)",
          className: "api-method get",
        },
      ],
    },
    {
      type: "category",
      label: "Shoppers",
      link: {
        type: "doc",
        id: "api-reference/rest/shoppers",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/find-shopper-by-email",
          label: "Find a shopper by e-mail",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/register-shopper",
          label: "Register a synthetic shopper",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/get-shopper",
          label: "Get a shopper profile with its saved addresses",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/add-saved-address",
          label: "Save an address on a shopper",
          className: "api-method post",
        },
      ],
    },
    {
      type: "category",
      label: "Operations",
      link: {
        type: "doc",
        id: "api-reference/rest/operations",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/get-health",
          label: "Liveness probe",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/get-readiness",
          label: "Readiness probe",
          className: "api-method get",
        },
      ],
    },
  ],
};

export default sidebar.apisidebar;
