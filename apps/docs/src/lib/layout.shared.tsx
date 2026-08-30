import type { BaseLayoutProps } from "fumadocs-ui/layouts/shared";

export function baseOptions(): BaseLayoutProps {
  return {
    nav: {
      title: (
        <>
          <span
            role="img"
            aria-label="Everstack"
            className="relative inline-block shrink-0 overflow-hidden"
            style={{ width: 23, height: 24 }}
          >
            <img
              src={`${import.meta.env.BASE_URL}everstack-mark-dark.png`}
              alt=""
              aria-hidden="true"
              className="absolute inset-0 size-full object-contain dark:hidden"
            />
            <img
              src={`${import.meta.env.BASE_URL}everstack-mark-light.png`}
              alt=""
              aria-hidden="true"
              className="absolute inset-0 size-full object-contain hidden dark:block"
            />
          </span>
          <span>Everstack</span>
        </>
      ),
      transparentMode: "top",
    },
    githubUrl: "https://github.com/everstackhq/everstack",
  };
}
