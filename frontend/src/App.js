import "./App.css";
import {
    useContext,
    createContext,
    React,
    useState,
    useRef,
    useEffect,
    useCallback,
    useMemo,
} from "react";
import {
    useParams,
    Link,
    NavLink,
    useNavigate,
    useLocation,
} from "react-router-dom";
import { throttle, debounce, filter } from "underscore";
import OutsideClickHandler from "react-outside-click-handler";
import { toast } from "react-toastify";
import moment from "moment";

const toastParams = {
    className:
        "fixed bottom-0 left-0 ml-4 mb-4 bg-white rounded-md shadow-md p-4",
    bodyClassName: "flex items-center",
    progressClassName: "w-full h-1 bg-gray-300 mt-2 rounded-full",
    position: "bottom-left",
    autoClose: 5000,
    hideProgressBar: true,
    closeOnClick: true,
    pauseOnHover: true,
    draggable: false,
    progress: undefined,
};

const tailwindConfig = {
    theme: {
        screens: {
            sm: 640,
            md: 768,
            lg: 1024,
            xl: 1280,
            "2xl": 1536,
        },
    },
};

function TopBar() {
    return (
        <div className="p-2 sm:px-5 flex justify-between sticky bg-gray-100 items-center">
            <div>
                <div className="flex flex-rows items-center">
                    <Link className="align-middle hover:underline" to="/">
                        Garden
                    </Link>
                </div>
            </div>
            <div>
                {false ? (
                    <div>
                        <span className="mr-2 cursor-pointer align-middle text-gray-500 hover:underline">
                            <Link to="/admin">Admin</Link>
                        </span>
                        <span className="mr-2 cursor-pointer align-middle text-gray-500 hover:underline">
                            Logout
                        </span>
                    </div>
                ) : (
                    <div className="p-1 text-small cursor-pointer rounded border align-middle hover:border-gray-400">
                        Use Garden
                    </div>
                )}
            </div>
        </div>
    );
}

function SideBar() {
    return (
        <div className="hidden sm:block sm:sticky sm:top-0 sm:px-6 sm:mr-4">
            <ul className="sm:sticky sm:top-5 list-none mb-1">
                <li className="mb-2">
                    <NavLink
                        className={({ isActive }) =>
                            isActive ? "text-blue-500" : "text-gray-500"
                        }
                        to="/seedlings"
                    >
                        <span className="w-full text-sm text-inherit text-gray-500 bg-gray-100 hover:bg-gray-300 font-bold py-2 px-4 rounded-full">
                            Seedlings
                        </span>
                    </NavLink>
                </li>
            </ul>
        </div>
    );
}

function HomePage() {
    return (
        <>
            <TopBar />
            <div className="flex mt-2">
                <SideBar />
                <SeedlingManager />
            </div>
        </>
    );
}

// A custom hook to handle fetch requests and errors
const useFetch = (url, options) => {
    const [data, setData] = useState(null);
    const [error, setError] = useState(null);
    const [loading, setLoading] = useState(false);

    useEffect(() => {
        const fetchData = async () => {
            setLoading(true);
            try {
                const response = await fetch(url, options);
                if (!response.ok) {
                    throw new Error(
                        `${response.status} ${response.statusText}`
                    );
                }
                const json = await response.json();
                setData(json);
                setError(null);
            } catch (err) {
                setData(null);
                setError(err.message);
            }
            setLoading(false);
        };
        fetchData();
    }, [url, options]);

    return { data, error, loading };
};

// A component to render a single seedling
const Seedling = ({ seedling, onDelete, onEdit }) => {
    return (
        <div className="bg-white shadow-lg rounded-lg p-4 m-2 flex flex-col">
            <h3 className="text-xl font-bold">{seedling.name}</h3>
            <p className="text-gray-600">{seedling.description}</p>
            <div>
                {seedling.step.includes("Complete") && (
                    <span className="text-green-500">Complete</span>
                )}
            </div>
            <div className="mt-auto flex justify-end space-x-2">
                <button
                    className="bg-blue-500 hover:bg-blue-700 text-white py-2 px-4 rounded"
                    onClick={() => onEdit(seedling)}
                >
                    Edit
                </button>
                <button
                    className="bg-red-500 hover:bg-red-700 text-white py-2 px-4 rounded"
                    onClick={() => onDelete(seedling.id)}
                >
                    Delete
                </button>
            </div>
        </div>
    );
};

// A component to render a list of seedlings
const SeedlingList = ({ seedlings, onDelete, onEdit }) => {
    return (
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
            {seedlings.map((seedling) => (
                <Seedling
                    key={seedling.id}
                    seedling={seedling}
                    onDelete={onDelete}
                    onEdit={onEdit}
                />
            ))}
        </div>
    );
};

// A component to render a form for creating or updating a seedling
const SeedlingForm = ({ seedling, onSubmit, onCancel }) => {
    const [name, setName] = useState(seedling ? seedling.name : "");
    const [description, setDescription] = useState(
        seedling ? seedling.description : ""
    );

    const handleSubmit = (e) => {
        e.preventDefault();
        onSubmit({
            id: seedling ? seedling.id : null,
            name,
            description,
        });
    };

    return (
        <form
            className="bg-white shadow-lg rounded-lg p-4 m-2"
            onSubmit={handleSubmit}
        >
            <div className="mb-4">
                <label
                    className="block text-gray-700 text-sm font-bold mb-2"
                    htmlFor="name"
                >
                    Name
                </label>
                <input
                    className="shadow appearance-none border rounded w-full py-2 px-3 text-gray-700 leading-tight focus:outline-none focus:shadow-outline"
                    id="name"
                    type="text"
                    placeholder="Enter seedling name"
                    value={name}
                    onChange={(e) => setName(e.target.value)}
                    required
                />
            </div>
            <div className="mb-4">
                <label
                    className="block text-gray-700 text-sm font-bold mb-2"
                    htmlFor="description"
                >
                    Description
                </label>
                <textarea
                    className="shadow appearance-none border rounded w-full py-2 px-3 text-gray-700 leading-tight focus:outline-none focus:shadow-outline"
                    id="description"
                    type="text"
                    placeholder="Enter seedling description"
                    value={description}
                    onChange={(e) => setDescription(e.target.value)}
                />
            </div>
            <div className="flex justify-end space-x-2">
                <button
                    className="bg-gray-500 hover:bg-gray-700 text-white py-2 px-4 rounded"
                    onClick={onCancel}
                >
                    Cancel
                </button>
                <button
                    className="bg-green-500 hover:bg-green-700 text-white py-2 px-4 rounded"
                    type="submit"
                >
                    {seedling ? "Update" : "Create"}
                </button>
            </div>
        </form>
    );
};

// A component to manage seedlings with CRUD operations
const SeedlingManager = () => {
    // The state for the current seedlings
    const [seedlings, setSeedlings] = useState([]);

    // The state for the current seedling to edit or null if creating a new one
    const [editing, setEditing] = useState(null);

    // The state for the fetch status and error
    const [status, setStatus] = useState("idle");
    const [error, setError] = useState(null);

    // The base URL for the CRUD API
    const baseURL = "http://localhost:3001/api/v1/seedlings";

    // A helper function to refresh the seedlings list
    const refreshSeedlings = () => {
        setStatus("loading");
        fetch(baseURL)
            .then((response) => {
                if (!response.ok) {
                    throw new Error(
                        `${response.status} ${response.statusText}`
                    );
                }
                return response.json();
            })
            .then((data) => {
                setSeedlings(data);
                setStatus("success");
                setError(null);
            })
            .catch((err) => {
                setSeedlings([]);
                setStatus("error");
                setError(err.message);
            });
    };

    // A helper function to handle create or update seedling
    const handleSave = (seedling) => {
        // Determine the fetch method and URL based on the id
        const method = seedling.id ? "PUT" : "POST";
        const url = seedling.id ? `${baseURL}/${seedling.id}` : baseURL;

        // Send the fetch request with the seedling data
        fetch(url, {
            method,
            headers: {
                "Content-Type": "application/json",
            },
            body: JSON.stringify(seedling),
        })
            .then((response) => {
                if (!response.ok) {
                    throw new Error(
                        `${response.status} ${response.statusText}`
                    );
                }
                return response.json();
            })
            .then((data) => {
                // Update the seedlings list with the new or updated seedling
                setSeedlings((prev) =>
                    prev
                        .map((s) => (s.id === data.id ? data : s))
                        .concat(data.id ? [] : data)
                );
                // Clear the editing state
                setEditing(null);
            })
            .catch((err) => {
                // Display the error message
                alert(err.message);
            });
    };

    // A helper function to handle delete seedling
    const handleDelete = (id) => {
        // Confirm the deletion
        if (!window.confirm("Are you sure you want to delete this seedling?")) {
            return;
        }

        // Send the fetch request with the id
        fetch(`${baseURL}/${id}`, {
            method: "DELETE",
        })
            .then((response) => {
                if (!response.ok) {
                    throw new Error(
                        `${response.status} ${response.statusText}`
                    );
                }
                return response.json();
            })
            .then((data) => {
                // Remove the deleted seedling from the list
                setSeedlings((prev) => prev.filter((s) => s.id !== id));
            })
            .catch((err) => {
                // Display the error message
                alert(err.message);
            });
    };

    // A helper function to handle edit seedling
    const handleEdit = (seedling) => {
        // Set the editing state with the seedling
        setEditing(seedling);
    };

    // A helper function to handle cancel editing or creating
    const handleCancel = () => {
        // Clear the editing state
        setEditing(null);
    };

    // Fetch the seedlings on mount
    useEffect(() => {
        refreshSeedlings();
    }, []);

    return (
        <div className="container mx-auto p-4">
            <h1 className="text-3xl font-bold mb-4">Seedling Manager</h1>
            {status === "loading" && <p>Loading...</p>}
            {status === "error" && <p>Error: {error}</p>}
            {status === "success" && (
                <>
                    <button
                        className="bg-yellow-500 hover:bg-yellow-700 text-white py-2 px-4 rounded mb-4"
                        onClick={() => setEditing({})}
                    >
                        Create New Seedling
                    </button>
                    {editing ? (
                        <SeedlingForm
                            seedling={editing}
                            onSubmit={handleSave}
                            onCancel={handleCancel}
                        />
                    ) : (
                        <SeedlingList
                            seedlings={seedlings}
                            onDelete={handleDelete}
                            onEdit={handleEdit}
                        />
                    )}
                </>
            )}
        </div>
    );
};

export { HomePage };
