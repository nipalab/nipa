type Handler = (event: { data: unknown }) => void;

export const Events = {
  On: (_name: string, _handler: Handler): (() => void) => () => {},
};
