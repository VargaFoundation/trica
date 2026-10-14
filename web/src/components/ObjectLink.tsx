import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router";
import { api } from "../api/endpoints";

/** A link to an object page. Without a display name it is looked up (and cached) from the API. */
export function ObjectLink({ id, display }: { id: string; display?: string }) {
  const lookup = useQuery({
    queryKey: ["object", id],
    queryFn: ({ signal }) => api.object(id, signal),
    enabled: display === undefined,
    staleTime: 5 * 60_000,
  });
  return (
    <Link to={`/objects/${encodeURIComponent(id)}`} className="object-link">
      {display ?? lookup.data?.display ?? id}
    </Link>
  );
}
